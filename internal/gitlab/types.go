package gitlab

import "time"

// User is a GitLab user.
type User struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
}

// MergeRequest holds the fields gl-dash reads from the merge request API.
type MergeRequest struct {
	ID           int       `json:"id"`
	IID          int       `json:"iid"`
	ProjectID    int       `json:"project_id"`
	Title        string    `json:"title"`
	Draft        bool      `json:"draft"`
	WebURL       string    `json:"web_url"`
	Author       User      `json:"author"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	HasConflicts bool      `json:"has_conflicts"`
	References   struct {
		Full string `json:"full"`
	} `json:"references"`
	// HeadPipeline is only set by the single merge request endpoint.
	HeadPipeline *Pipeline `json:"head_pipeline"`
}

// Pipeline is a CI pipeline.
type Pipeline struct {
	ID     int    `json:"id"`
	Status string `json:"status"`
	WebURL string `json:"web_url"`
}

// Approvals is the approval state of a merge request.
type Approvals struct {
	ApprovalsRequired int `json:"approvals_required"`
	ApprovalsLeft     int `json:"approvals_left"`
	ApprovedBy        []struct {
		User User `json:"user"`
	} `json:"approved_by"`
}

// Reviewer is a reviewer of a merge request and their review state:
// unreviewed, review_started, reviewed, requested_changes, approved or unapproved.
type Reviewer struct {
	User  User   `json:"user"`
	State string `json:"state"`
}

// Discussion is a merge request discussion thread.
type Discussion struct {
	Notes []struct {
		Resolvable bool `json:"resolvable"`
		Resolved   bool `json:"resolved"`
	} `json:"notes"`
}

// unresolved reports whether the thread has a resolvable note that is not resolved.
func (d Discussion) unresolved() bool {
	for _, n := range d.Notes {
		if n.Resolvable && !n.Resolved {
			return true
		}
	}
	return false
}

// Job is a CI job.
type Job struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Stage         string `json:"stage"`
	Status        string `json:"status"`
	AllowFailure  bool   `json:"allow_failure"`
	FailureReason string `json:"failure_reason"`
	WebURL        string `json:"web_url"`
}
