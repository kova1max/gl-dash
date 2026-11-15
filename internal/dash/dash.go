// Package dash loads dashboard rows from one or more GitLab hosts.
package dash

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kova1max/gl-dash/internal/config"
	"github.com/kova1max/gl-dash/internal/errline"
	"github.com/kova1max/gl-dash/internal/gitlab"
)

// Key identifies a merge request across hosts.
type Key struct {
	Host      string
	ProjectID int
	IID       int
}

// Row is one merge request in a section.
type Row struct {
	Key
	Project   string
	Title     string
	Author    string
	WebURL    string
	Draft     bool
	Conflicts bool
	Updated   time.Time
}

// Detail is what the list endpoint does not return, fetched per row.
type Detail struct {
	Pipeline          string // head pipeline status, "" when there is none
	PipelineID        int
	ApprovalsGiven    int
	ApprovalsRequired int
	Unresolved        int
}

// Blocker explains a failed pipeline: its first failed job and that job's
// first error line. Line may be empty when no line could be identified.
type Blocker struct {
	Job  string
	Line string
}

// Source is a connected GitLab host.
type Source struct {
	Host   config.Host
	User   string
	client *gitlab.Client
}

// Connect checks the host's token and finds out who it belongs to.
func Connect(ctx context.Context, h config.Host) (*Source, error) {
	if h.Token == "" {
		return nil, fmt.Errorf("%s: no token; %s", h.Name, config.TokenHint(h.Name))
	}
	c := gitlab.New(h.APIProtocol, h.APIHost, h.Token)
	u, err := c.CurrentUser(ctx)
	if gitlab.IsUnauthorized(err) {
		return nil, fmt.Errorf("%s: token rejected (401); %s", h.Name, config.TokenHint(h.Name))
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w", h.Name, err)
	}
	return &Source{Host: h, User: u.Username, client: c}, nil
}

// maxPages caps how many list pages one section reads per host.
const maxPages = 5

// Section lists the merge requests on this host that match f. Review-state
// filters run on the client, so pages are read until limit rows are kept,
// the pages run out or maxPages pages were read.
func (s *Source) Section(ctx context.Context, f Filter, limit int) ([]Row, error) {
	q := f.Query(s.User, limit)
	var rows []Row
	seen := map[Key]bool{}
	for page, read := 1, 0; page != 0 && read < maxPages && len(rows) < limit; read++ {
		q.Set("page", strconv.Itoa(page))
		mrs, next, err := s.client.ListMergeRequests(ctx, q)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", s.Host.Name, err)
		}
		if f.needsReviewers() {
			if mrs, err = s.reviewed(ctx, f, mrs); err != nil {
				return nil, fmt.Errorf("%s: %w", s.Host.Name, err)
			}
		}
		for _, mr := range mrs {
			k := Key{Host: s.Host.Name, ProjectID: mr.ProjectID, IID: mr.IID}
			if seen[k] {
				continue
			}
			seen[k] = true
			project, _, _ := strings.Cut(mr.References.Full, "!")
			rows = append(rows, Row{
				Key:       k,
				Project:   project,
				Title:     mr.Title,
				Author:    mr.Author.Username,
				WebURL:    mr.WebURL,
				Draft:     mr.Draft,
				Conflicts: mr.HasConflicts,
				Updated:   mr.UpdatedAt,
			})
		}
		page = next
	}
	if len(rows) > limit {
		rows = rows[:limit]
	}
	return rows, nil
}

// reviewed keeps the merge requests whose reviewer states match f.
func (s *Source) reviewed(ctx context.Context, f Filter, mrs []gitlab.MergeRequest) ([]gitlab.MergeRequest, error) {
	keep := make([]bool, len(mrs))
	errs := make([]error, len(mrs))
	var wg sync.WaitGroup
	for i, mr := range mrs {
		wg.Go(func() {
			rs, err := s.client.Reviewers(ctx, mr.ProjectID, mr.IID)
			if err != nil {
				errs[i] = err
				return
			}
			var revs []reviewer
			for _, r := range rs {
				st := r.State
				if st == "" {
					st = "unreviewed"
				}
				revs = append(revs, reviewer{r.User.Username, st})
			}
			keep[i] = f.keep(s.User, revs)
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	var out []gitlab.MergeRequest
	for i, mr := range mrs {
		if keep[i] {
			out = append(out, mr)
		}
	}
	return out, nil
}

// Detail fetches pipeline, approval and thread state for one row.
func (s *Source) Detail(ctx context.Context, k Key) (Detail, error) {
	var (
		d                   Detail
		mr                  gitlab.MergeRequest
		ap                  gitlab.Approvals
		errMR, errAp, errTh error
		wg                  sync.WaitGroup
	)
	wg.Go(func() { mr, errMR = s.client.MergeRequest(ctx, k.ProjectID, k.IID) })
	wg.Go(func() { ap, errAp = s.client.Approvals(ctx, k.ProjectID, k.IID) })
	wg.Go(func() { d.Unresolved, errTh = s.client.UnresolvedThreads(ctx, k.ProjectID, k.IID) })
	wg.Wait()
	if err := errors.Join(errMR, errAp, errTh); err != nil {
		return d, err
	}
	if mr.HeadPipeline != nil {
		d.Pipeline, d.PipelineID = mr.HeadPipeline.Status, mr.HeadPipeline.ID
	}
	d.ApprovalsGiven, d.ApprovalsRequired = len(ap.ApprovedBy), ap.ApprovalsRequired
	return d, nil
}

// Blocker finds the first failed job of a pipeline and its first error line.
// Jobs allowed to fail are ignored.
func (s *Source) Blocker(ctx context.Context, projectID, pipelineID int) (Blocker, error) {
	jobs, err := s.client.FailedJobs(ctx, projectID, pipelineID)
	if err != nil {
		return Blocker{}, err
	}
	jobs = slices.DeleteFunc(jobs, func(j gitlab.Job) bool { return j.AllowFailure })
	if len(jobs) == 0 {
		return Blocker{}, nil
	}
	// The lowest job ID is the earliest created, so the earliest stage.
	first := slices.MinFunc(jobs, func(a, b gitlab.Job) int { return a.ID - b.ID })
	trace, err := s.client.JobTrace(ctx, projectID, first.ID)
	if err != nil {
		return Blocker{Job: first.Name}, err
	}
	return Blocker{Job: first.Name, Line: errline.Find(trace)}, nil
}

// SortRows orders rows across hosts, most recently updated first.
func SortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].Updated.Equal(rows[j].Updated) {
			return rows[i].Updated.After(rows[j].Updated)
		}
		if rows[i].Host != rows[j].Host {
			return rows[i].Host < rows[j].Host
		}
		return rows[i].IID > rows[j].IID
	})
}
