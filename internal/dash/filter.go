package dash

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
)

// Filter is a parsed section filter. Most keys map to GitLab's
// GET /merge_requests parameters; review states are checked per merge
// request because the list endpoint cannot filter on them.
type Filter struct {
	params          url.Values // may contain "@me"
	labels          []string
	MyReviewStates  []string // your own review state as a reviewer
	AnyReviewStates []string // any other reviewer's review state
}

var reviewStates = []string{"unreviewed", "review_started", "reviewed", "requested_changes", "approved", "unapproved"}

var paramKeys = map[string]string{
	"author":   "author_username",
	"assignee": "assignee_username",
	"reviewer": "reviewer_username",
	"state":    "state",
	"draft":    "draft",
}

// ParseFilter parses a filter like "reviewer:@me draft:false".
func ParseFilter(s string) (Filter, error) {
	f := Filter{params: url.Values{"state": {"opened"}}}
	for _, tok := range strings.Fields(s) {
		key, val, ok := strings.Cut(tok, ":")
		if !ok || val == "" {
			return f, fmt.Errorf("filter %q: want key:value", tok)
		}
		switch key {
		case "author", "assignee", "reviewer":
			f.params.Set(paramKeys[key], val)
		case "state":
			if !slices.Contains([]string{"opened", "closed", "merged", "locked", "all"}, val) {
				return f, fmt.Errorf("filter %q: state is opened, closed, merged, locked or all", tok)
			}
			f.params.Set("state", val)
		case "draft":
			if val != "true" && val != "false" {
				return f, fmt.Errorf("filter %q: draft is true or false", tok)
			}
			f.params.Set("draft", val)
		case "label":
			f.labels = append(f.labels, val)
		case "review-state", "my-review-state":
			states := strings.Split(val, ",")
			for _, st := range states {
				if !slices.Contains(reviewStates, st) {
					return f, fmt.Errorf("filter %q: unknown review state %q (want %s)", tok, st, strings.Join(reviewStates, ", "))
				}
			}
			if key == "review-state" {
				f.AnyReviewStates = states
			} else {
				f.MyReviewStates = states
			}
		default:
			return f, fmt.Errorf("filter %q: unknown key %q (want author, assignee, reviewer, state, draft, label, review-state, my-review-state)", tok, key)
		}
	}
	return f, nil
}

// Query returns the GET /merge_requests parameters with @me replaced by me.
func (f Filter) Query(me string, limit int) url.Values {
	q := url.Values{
		"scope":    {"all"},
		"order_by": {"updated_at"},
		"sort":     {"desc"},
		"per_page": {fmt.Sprint(limit)},
	}
	for k, vs := range f.params {
		for _, v := range vs {
			if v == "@me" {
				v = me
			}
			q.Set(k, v)
		}
	}
	if len(f.labels) > 0 {
		q.Set("labels", strings.Join(f.labels, ","))
	}
	return q
}

// needsReviewers reports whether rows must be checked against reviewer states.
func (f Filter) needsReviewers() bool {
	return len(f.MyReviewStates) > 0 || len(f.AnyReviewStates) > 0
}

// keep applies the review-state filters to one merge request's reviewers.
// Reviewers without a state (older GitLab) count as unreviewed.
func (f Filter) keep(me string, reviewers []reviewer) bool {
	if len(f.MyReviewStates) > 0 {
		mine := false
		for _, r := range reviewers {
			if r.username == me && slices.Contains(f.MyReviewStates, r.state) {
				mine = true
			}
		}
		if !mine {
			return false
		}
	}
	if len(f.AnyReviewStates) > 0 {
		for _, r := range reviewers {
			if r.username != me && slices.Contains(f.AnyReviewStates, r.state) {
				return true
			}
		}
		return false
	}
	return true
}

type reviewer struct{ username, state string }
