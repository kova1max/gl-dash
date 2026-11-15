// Package gitlab is a small read-only client for the GitLab REST API v4.
// It only issues GET requests.
package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// maxTraceBytes caps how much of a job log is read.
const maxTraceBytes = 4 << 20

// maxConcurrent caps in-flight requests per host.
const maxConcurrent = 8

// Client talks to one GitLab instance.
type Client struct {
	base  string // e.g. https://gitlab.com/api/v4
	token string
	http  *http.Client
	sem   chan struct{}
}

// New returns a client for the API at protocol://apiHost/api/v4.
func New(protocol, apiHost, token string) *Client {
	if protocol == "" {
		protocol = "https"
	}
	return &Client{
		base:  protocol + "://" + strings.TrimSuffix(apiHost, "/") + "/api/v4",
		token: token,
		http:  &http.Client{Timeout: 30 * time.Second},
		sem:   make(chan struct{}, maxConcurrent),
	}
}

// StatusError is returned for non-2xx responses.
type StatusError struct {
	Code int
	Path string
}

func (e *StatusError) Error() string {
	return fmt.Sprintf("GET %s: %d %s", e.Path, e.Code, http.StatusText(e.Code))
}

// IsUnauthorized reports whether err is a 401 response.
func IsUnauthorized(err error) bool {
	var se *StatusError
	return errors.As(err, &se) && se.Code == http.StatusUnauthorized
}

func (c *Client) get(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	select {
	case c.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	u := c.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		<-c.sem
		return nil, err
	}
	// GitLab accepts personal, project, group and OAuth tokens as Bearer.
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	<-c.sem
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		_ = resp.Body.Close()
		return nil, &StatusError{Code: resp.StatusCode, Path: path}
	}
	return resp, nil
}

// getJSON decodes one page into out and returns the next page number, or 0.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) (int, error) {
	resp, err := c.get(ctx, path, q)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return 0, fmt.Errorf("GET %s: decode: %w", path, err)
	}
	next, _ := strconv.Atoi(resp.Header.Get("X-Next-Page"))
	return next, nil
}

func mrPath(projectID, iid int) string {
	return fmt.Sprintf("/projects/%d/merge_requests/%d", projectID, iid)
}

// CurrentUser returns the user the token belongs to.
func (c *Client) CurrentUser(ctx context.Context) (User, error) {
	var u User
	_, err := c.getJSON(ctx, "/user", nil, &u)
	return u, err
}

// ListMergeRequests calls GET /merge_requests with the given filters.
// It returns one page and the next page number, or 0; callers set per_page
// and page.
func (c *Client) ListMergeRequests(ctx context.Context, q url.Values) ([]MergeRequest, int, error) {
	var mrs []MergeRequest
	next, err := c.getJSON(ctx, "/merge_requests", q, &mrs)
	return mrs, next, err
}

// MergeRequest returns one merge request with its head pipeline.
func (c *Client) MergeRequest(ctx context.Context, projectID, iid int) (MergeRequest, error) {
	var mr MergeRequest
	_, err := c.getJSON(ctx, mrPath(projectID, iid), nil, &mr)
	return mr, err
}

// Approvals returns the approval state of a merge request.
func (c *Client) Approvals(ctx context.Context, projectID, iid int) (Approvals, error) {
	var a Approvals
	_, err := c.getJSON(ctx, mrPath(projectID, iid)+"/approvals", nil, &a)
	return a, err
}

// Reviewers returns the reviewers of a merge request and their review state.
func (c *Client) Reviewers(ctx context.Context, projectID, iid int) ([]Reviewer, error) {
	var rs []Reviewer
	_, err := c.getJSON(ctx, mrPath(projectID, iid)+"/reviewers", nil, &rs)
	return rs, err
}

// UnresolvedThreads counts resolvable discussion threads that are not resolved.
func (c *Client) UnresolvedThreads(ctx context.Context, projectID, iid int) (int, error) {
	n := 0
	for page := 1; page != 0; {
		var ds []Discussion
		q := url.Values{"per_page": {"100"}, "page": {strconv.Itoa(page)}}
		next, err := c.getJSON(ctx, mrPath(projectID, iid)+"/discussions", q, &ds)
		if err != nil {
			return 0, err
		}
		for _, d := range ds {
			if d.unresolved() {
				n++
			}
		}
		page = next
	}
	return n, nil
}

// FailedJobs returns the failed jobs of a pipeline.
func (c *Client) FailedJobs(ctx context.Context, projectID, pipelineID int) ([]Job, error) {
	var jobs []Job
	q := url.Values{"scope[]": {"failed"}, "per_page": {"100"}}
	_, err := c.getJSON(ctx, fmt.Sprintf("/projects/%d/pipelines/%d/jobs", projectID, pipelineID), q, &jobs)
	return jobs, err
}

// JobTrace returns up to the first 4 MiB of a job log.
func (c *Client) JobTrace(ctx context.Context, projectID, jobID int) (string, error) {
	resp, err := c.get(ctx, fmt.Sprintf("/projects/%d/jobs/%d/trace", projectID, jobID), nil)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxTraceBytes))
	return string(b), err
}
