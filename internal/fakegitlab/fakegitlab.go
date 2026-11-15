// Package fakegitlab serves a seeded, read-only subset of the GitLab REST API
// v4. Tests, the demo recording and the benchmark use it so nothing ever
// talks to a real GitLab.
package fakegitlab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Dataset is the seeded state of one fake GitLab instance.
type Dataset struct {
	Host  string `json:"host"`  // used in web URLs
	Token string `json:"token"` // the only accepted token
	User  string `json:"user"`  // username the token belongs to
	MRs   []MR   `json:"mrs"`
}

// MR is a seeded merge request.
type MR struct {
	ProjectID         int        `json:"project_id"`
	Project           string     `json:"project"`
	IID               int        `json:"iid"`
	Title             string     `json:"title"`
	Author            string     `json:"author"`
	State             string     `json:"state"` // default opened
	Draft             bool       `json:"draft"`
	Labels            []string   `json:"labels"`
	Assignees         []string   `json:"assignees"`
	Reviewers         []Reviewer `json:"reviewers"`
	Created           Ago        `json:"created"`
	Updated           Ago        `json:"updated"`
	HasConflicts      bool       `json:"has_conflicts"`
	ApprovalsRequired int        `json:"approvals_required"`
	ApprovedBy        []string   `json:"approved_by"`
	Unresolved        int        `json:"unresolved_threads"`
	Resolved          int        `json:"resolved_threads"`
	Pipeline          *Pipeline  `json:"pipeline"`
}

// Reviewer is a seeded reviewer and their review state.
type Reviewer struct {
	Username string `json:"username"`
	State    string `json:"state"`
}

// Pipeline is a seeded head pipeline.
type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	Jobs   []Job  `json:"jobs"`
}

// Job is a seeded CI job. Trace is the raw log; TraceFile is read relative
// to the dataset file when Trace is empty.
type Job struct {
	ID           int    `json:"id"`
	Name         string `json:"name"`
	Stage        string `json:"stage"`
	Status       string `json:"status"`
	AllowFailure bool   `json:"allow_failure"`
	Trace        string `json:"trace"`
	TraceFile    string `json:"trace_file"`
}

// Ago is a duration before the server's clock, written like "3h" or "2d".
type Ago time.Duration

// UnmarshalJSON accepts Go durations plus a "d" suffix for days.
func (a *Ago) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if n, ok := strings.CutSuffix(s, "d"); ok {
		days, err := strconv.Atoi(n)
		if err != nil {
			return fmt.Errorf("bad duration %q", s)
		}
		*a = Ago(time.Duration(days) * 24 * time.Hour)
		return nil
	}
	d, err := time.ParseDuration(s)
	*a = Ago(d)
	return err
}

// Load reads a dataset from a JSON file and inlines trace files.
func Load(path string) (Dataset, error) {
	var ds Dataset
	b, err := os.ReadFile(path)
	if err != nil {
		return ds, err
	}
	if err := json.Unmarshal(b, &ds); err != nil {
		return ds, fmt.Errorf("%s: %w", path, err)
	}
	dir := filepath.Dir(path)
	for i := range ds.MRs {
		p := ds.MRs[i].Pipeline
		if p == nil {
			continue
		}
		for j := range p.Jobs {
			if p.Jobs[j].Trace == "" && p.Jobs[j].TraceFile != "" {
				t, err := os.ReadFile(filepath.Join(dir, p.Jobs[j].TraceFile))
				if err != nil {
					return ds, err
				}
				p.Jobs[j].Trace = string(t)
			}
		}
	}
	return ds, nil
}

// Server is an http.Handler for one fake instance.
type Server struct {
	ds       Dataset
	now      time.Time
	latency  time.Duration
	requests atomic.Int64
	users    map[string]int
}

// New returns a server for ds. Relative times are measured back from now.
// Every request sleeps for latency first, to simulate a real network.
func New(ds Dataset, now time.Time, latency time.Duration) *Server {
	s := &Server{ds: ds, now: now, latency: latency, users: map[string]int{}}
	s.userID(ds.User)
	for _, mr := range ds.MRs {
		s.userID(mr.Author)
		for _, r := range mr.Reviewers {
			s.userID(r.Username)
		}
		for _, a := range mr.Assignees {
			s.userID(a)
		}
		for _, a := range mr.ApprovedBy {
			s.userID(a)
		}
	}
	return s
}

// Requests returns how many API requests the server has handled.
func (s *Server) Requests() int64 { return s.requests.Load() }

func (s *Server) userID(name string) int {
	if id, ok := s.users[name]; ok {
		return id
	}
	s.users[name] = len(s.users) + 1
	return s.users[name]
}

func (s *Server) user(name string) map[string]any {
	return map[string]any{"id": s.users[name], "username": name, "name": name}
}

var (
	mrRe     = regexp.MustCompile(`^/api/v4/projects/(\d+)/merge_requests/(\d+)(/approvals|/reviewers|/discussions)?$`)
	jobsRe   = regexp.MustCompile(`^/api/v4/projects/(\d+)/pipelines/(\d+)/jobs$`)
	traceRe  = regexp.MustCompile(`^/api/v4/projects/(\d+)/jobs/(\d+)/trace$`)
	bearerRe = regexp.MustCompile(`^Bearer (.+)$`)
)

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.requests.Add(1)
	if s.latency > 0 {
		time.Sleep(s.latency)
	}
	if r.Method != http.MethodGet {
		http.Error(w, `{"message":"405 Method Not Allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	tok := r.Header.Get("PRIVATE-TOKEN")
	if m := bearerRe.FindStringSubmatch(r.Header.Get("Authorization")); m != nil {
		tok = m[1]
	}
	if tok != s.ds.Token {
		http.Error(w, `{"message":"401 Unauthorized"}`, http.StatusUnauthorized)
		return
	}
	p := r.URL.Path
	switch {
	case p == "/api/v4/user":
		writeJSON(w, s.user(s.ds.User))
	case p == "/api/v4/merge_requests":
		s.list(w, r)
	case mrRe.MatchString(p):
		m := mrRe.FindStringSubmatch(p)
		mr := s.find(atoi(m[1]), atoi(m[2]))
		if mr == nil {
			http.NotFound(w, r)
			return
		}
		switch m[3] {
		case "":
			writeJSON(w, s.mrJSON(mr, true))
		case "/approvals":
			writeJSON(w, s.approvals(mr))
		case "/reviewers":
			writeJSON(w, s.reviewers(mr))
		case "/discussions":
			s.discussions(w, r, mr)
		}
	case jobsRe.MatchString(p):
		m := jobsRe.FindStringSubmatch(p)
		writeJSON(w, s.jobs(atoi(m[1]), atoi(m[2]), r.URL.Query()["scope[]"]))
	case traceRe.MatchString(p):
		m := traceRe.FindStringSubmatch(p)
		job := s.job(atoi(m[1]), atoi(m[2]))
		if job == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(job.Trace))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	me := s.ds.User
	var out []*MR
	for i := range s.ds.MRs {
		mr := &s.ds.MRs[i]
		state := mr.State
		if state == "" {
			state = "opened"
		}
		if v := q.Get("state"); v != "" && v != "all" && v != state {
			continue
		}
		switch q.Get("scope") {
		case "", "created_by_me":
			if mr.Author != me {
				continue
			}
		case "assigned_to_me":
			if !slices.Contains(mr.Assignees, me) {
				continue
			}
		case "reviews_for_me":
			if !hasReviewer(mr, me) {
				continue
			}
		}
		if v := q.Get("author_username"); v != "" && mr.Author != v {
			continue
		}
		if v := q.Get("assignee_username"); v != "" && !slices.Contains(mr.Assignees, v) {
			continue
		}
		if v := q.Get("reviewer_username"); v != "" && !hasReviewer(mr, v) {
			continue
		}
		if v := q.Get("draft"); v != "" && strconv.FormatBool(mr.Draft) != v {
			continue
		}
		if v := q.Get("labels"); v != "" && !hasAll(mr.Labels, strings.Split(v, ",")) {
			continue
		}
		out = append(out, mr)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Updated < out[j].Updated })
	per := 20
	if n, err := strconv.Atoi(q.Get("per_page")); err == nil && n > 0 {
		per = n
	}
	page := max(atoi(q.Get("page")), 1)
	lo, hi := min((page-1)*per, len(out)), min(page*per, len(out))
	if hi < len(out) {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	res := []map[string]any{}
	for _, mr := range out[lo:hi] {
		res = append(res, s.mrJSON(mr, false))
	}
	writeJSON(w, res)
}

func (s *Server) mrJSON(mr *MR, single bool) map[string]any {
	state := mr.State
	if state == "" {
		state = "opened"
	}
	m := map[string]any{
		"id":            mr.ProjectID*1000 + mr.IID,
		"iid":           mr.IID,
		"project_id":    mr.ProjectID,
		"title":         mr.Title,
		"state":         state,
		"draft":         mr.Draft,
		"labels":        orEmpty(mr.Labels),
		"web_url":       fmt.Sprintf("https://%s/%s/-/merge_requests/%d", s.ds.Host, mr.Project, mr.IID),
		"author":        s.user(mr.Author),
		"created_at":    s.now.Add(-time.Duration(mr.Created)).UTC().Format(time.RFC3339),
		"updated_at":    s.now.Add(-time.Duration(mr.Updated)).UTC().Format(time.RFC3339),
		"has_conflicts": mr.HasConflicts,
		"references":    map[string]string{"full": fmt.Sprintf("%s!%d", mr.Project, mr.IID)},
	}
	if single {
		var hp any
		if mr.Pipeline != nil {
			hp = map[string]any{
				"id":      mr.Pipeline.ID,
				"status":  mr.Pipeline.Status,
				"web_url": fmt.Sprintf("https://%s/%s/-/pipelines/%d", s.ds.Host, mr.Project, mr.Pipeline.ID),
			}
		}
		m["head_pipeline"] = hp
	}
	return m
}

func (s *Server) approvals(mr *MR) map[string]any {
	by := []map[string]any{}
	for _, u := range mr.ApprovedBy {
		by = append(by, map[string]any{"user": s.user(u)})
	}
	left := max(mr.ApprovalsRequired-len(mr.ApprovedBy), 0)
	return map[string]any{
		"approvals_required": mr.ApprovalsRequired,
		"approvals_left":     left,
		"approved":           left == 0,
		"approved_by":        by,
	}
}

func (s *Server) reviewers(mr *MR) []map[string]any {
	out := []map[string]any{}
	for _, r := range mr.Reviewers {
		out = append(out, map[string]any{"user": s.user(r.Username), "state": r.State})
	}
	return out
}

// discussions returns one non-resolvable comment thread plus the seeded
// resolved and unresolved threads, paginated like GitLab.
func (s *Server) discussions(w http.ResponseWriter, r *http.Request, mr *MR) {
	all := []map[string]any{{"id": "c0", "notes": []map[string]any{{"resolvable": false, "resolved": false}}}}
	for i := 0; i < mr.Resolved; i++ {
		all = append(all, map[string]any{"id": fmt.Sprintf("r%d", i), "notes": []map[string]any{{"resolvable": true, "resolved": true}}})
	}
	for i := 0; i < mr.Unresolved; i++ {
		all = append(all, map[string]any{"id": fmt.Sprintf("u%d", i), "notes": []map[string]any{{"resolvable": true, "resolved": false}}})
	}
	per := 20
	if n, err := strconv.Atoi(r.URL.Query().Get("per_page")); err == nil && n > 0 {
		per = n
	}
	page := max(atoi(r.URL.Query().Get("page")), 1)
	lo, hi := min((page-1)*per, len(all)), min(page*per, len(all))
	if hi < len(all) {
		w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
	}
	writeJSON(w, all[lo:hi])
}

func (s *Server) jobs(projectID, pipelineID int, scopes []string) []map[string]any {
	out := []map[string]any{}
	for i := range s.ds.MRs {
		mr := &s.ds.MRs[i]
		if mr.ProjectID != projectID || mr.Pipeline == nil || mr.Pipeline.ID != pipelineID {
			continue
		}
		// GitLab lists jobs newest first.
		for j := len(mr.Pipeline.Jobs) - 1; j >= 0; j-- {
			job := mr.Pipeline.Jobs[j]
			if len(scopes) > 0 && !slices.Contains(scopes, job.Status) {
				continue
			}
			out = append(out, map[string]any{
				"id": job.ID, "name": job.Name, "stage": job.Stage, "status": job.Status,
				"allow_failure": job.AllowFailure, "failure_reason": failureReason(job.Status),
				"web_url": fmt.Sprintf("https://%s/%s/-/jobs/%d", s.ds.Host, mr.Project, job.ID),
			})
		}
	}
	return out
}

func failureReason(status string) string {
	if status == "failed" {
		return "script_failure"
	}
	return ""
}

func (s *Server) job(projectID, jobID int) *Job {
	for i := range s.ds.MRs {
		mr := &s.ds.MRs[i]
		if mr.ProjectID != projectID || mr.Pipeline == nil {
			continue
		}
		for j := range mr.Pipeline.Jobs {
			if mr.Pipeline.Jobs[j].ID == jobID {
				return &mr.Pipeline.Jobs[j]
			}
		}
	}
	return nil
}

func (s *Server) find(projectID, iid int) *MR {
	for i := range s.ds.MRs {
		if s.ds.MRs[i].ProjectID == projectID && s.ds.MRs[i].IID == iid {
			return &s.ds.MRs[i]
		}
	}
	return nil
}

func hasReviewer(mr *MR, name string) bool {
	return slices.ContainsFunc(mr.Reviewers, func(r Reviewer) bool { return r.Username == name })
}

func hasAll(have, want []string) bool {
	for _, w := range want {
		if !slices.Contains(have, w) {
			return false
		}
	}
	return true
}

func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
