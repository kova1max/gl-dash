package dash

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kova1max/gl-dash/internal/config"
	"github.com/kova1max/gl-dash/internal/fakegitlab"
)

const goTestTrace = "section_start:1:step_script\r\x1b[0K\n$ go test ./...\n--- FAIL: TestCreateOrder (0.00s)\n    orders_test.go:88: expected 200, got 500\nFAIL\nsection_end:1:step_script\r\x1b[0K\nERROR: Job failed: exit code 1\n"

func ago(d time.Duration) fakegitlab.Ago { return fakegitlab.Ago(d) }

func comDataset() fakegitlab.Dataset {
	return fakegitlab.Dataset{
		Host: "gitlab.com", Token: "com-token", User: "alex",
		MRs: []fakegitlab.MR{
			{
				ProjectID: 1, Project: "acme/shop", IID: 42, Title: "Add order API", Author: "sam",
				Reviewers: []fakegitlab.Reviewer{{Username: "alex", State: "unreviewed"}},
				Created:   ago(48 * time.Hour), Updated: ago(time.Hour),
				ApprovalsRequired: 2, ApprovedBy: []string{"kim"}, Unresolved: 2, Resolved: 1,
				Pipeline: &fakegitlab.Pipeline{ID: 900, Status: "failed", Jobs: []fakegitlab.Job{
					{ID: 1, Name: "lint", Stage: "lint", Status: "success"},
					{ID: 2, Name: "test:unit", Stage: "test", Status: "failed", Trace: goTestTrace},
					{ID: 3, Name: "test:flaky", Stage: "test", Status: "failed", AllowFailure: true, Trace: "Error: flaky\n"},
				}},
			},
			{
				// Already reviewed by alex: not in "Review requested".
				ProjectID: 1, Project: "acme/shop", IID: 40, Title: "Bump deps", Author: "kim",
				Reviewers: []fakegitlab.Reviewer{{Username: "alex", State: "approved"}},
				Created:   ago(72 * time.Hour), Updated: ago(3 * time.Hour),
			},
			{
				ProjectID: 2, Project: "acme/web", IID: 7, Title: "Dark mode", Author: "alex",
				Reviewers: []fakegitlab.Reviewer{{Username: "sam", State: "requested_changes"}},
				Created:   ago(24 * time.Hour), Updated: ago(30 * time.Minute), HasConflicts: true,
				Pipeline: &fakegitlab.Pipeline{ID: 901, Status: "success"},
			},
			{
				ProjectID: 2, Project: "acme/web", IID: 8, Title: "Draft: new nav", Author: "alex", Draft: true,
				Reviewers: []fakegitlab.Reviewer{{Username: "sam", State: "reviewed"}},
				Created:   ago(5 * time.Hour), Updated: ago(4 * time.Hour),
			},
		},
	}
}

func devDataset() fakegitlab.Dataset {
	return fakegitlab.Dataset{
		Host: "gitlab.example.dev", Token: "dev-token", User: "alex.k",
		MRs: []fakegitlab.MR{
			{
				ProjectID: 10, Project: "platform/billing", IID: 3, Title: "Retry webhooks", Author: "jo",
				Reviewers: []fakegitlab.Reviewer{{Username: "alex.k", State: "review_started"}},
				Created:   ago(10 * time.Hour), Updated: ago(2 * time.Hour),
				Pipeline: &fakegitlab.Pipeline{ID: 77, Status: "running"},
			},
		},
	}
}

type fakeHost struct {
	host config.Host
	srv  *fakegitlab.Server
}

func startHost(t *testing.T, ds fakegitlab.Dataset, now time.Time) fakeHost {
	t.Helper()
	srv := fakegitlab.New(ds, now, 0)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return fakeHost{
		host: config.Host{Name: ds.Host, APIHost: strings.TrimPrefix(ts.URL, "http://"), APIProtocol: "http", Token: ds.Token},
		srv:  srv,
	}
}

func TestRunAllAcrossHosts(t *testing.T) {
	now := time.Now()
	com, dev := startHost(t, comDataset(), now), startHost(t, devDataset(), now)
	st, err := NewState([]config.Host{com.host, dev.host}, config.DefaultSections)
	if err != nil {
		t.Fatal(err)
	}
	for i := range st.Sections {
		st.Sections[i].Limit = 20
	}
	RunAll(context.Background(), st)

	got := map[string][]string{}
	for _, s := range st.Sections {
		if len(s.Errs) > 0 || s.Loading != 0 {
			t.Fatalf("section %q: errs=%v loading=%d", s.Title, s.Errs, s.Loading)
		}
		for _, r := range s.Rows {
			got[s.Title] = append(got[s.Title], r.Host+" "+r.Project+"!"+strconv.Itoa(r.IID))
		}
	}
	want := map[string][]string{
		// Newest first across both hosts.
		"Review requested":    {"gitlab.com acme/shop!42", "gitlab.example.dev platform/billing!3"},
		"Returned to you":     {"gitlab.com acme/web!7"},
		"Your merge requests": {"gitlab.com acme/web!7", "gitlab.com acme/web!8"},
	}
	for title, rows := range want {
		if strings.Join(got[title], ",") != strings.Join(rows, ",") {
			t.Errorf("%s = %v, want %v", title, got[title], rows)
		}
	}

	k := Key{Host: "gitlab.com", ProjectID: 1, IID: 42}
	if d := st.Details[k]; d != (Detail{Pipeline: "failed", PipelineID: 900, ApprovalsGiven: 1, ApprovalsRequired: 2, Unresolved: 2}) {
		t.Errorf("detail = %+v", d)
	}
	if b := st.Blockers[k]; b != (Blocker{Job: "test:unit", Line: "expected 200, got 500 (orders_test.go:88)"}) {
		t.Errorf("blocker = %+v", b)
	}
	if _, ok := st.Blockers[Key{Host: "gitlab.com", ProjectID: 2, IID: 7}]; ok {
		t.Error("blocker fetched for a passing pipeline")
	}
	if d := st.Details[Key{Host: "gitlab.example.dev", ProjectID: 10, IID: 3}]; d.Pipeline != "running" {
		t.Errorf("dev detail = %+v", d)
	}
}

func TestBadTokenKeepsOtherHosts(t *testing.T) {
	now := time.Now()
	com, dev := startHost(t, comDataset(), now), startHost(t, devDataset(), now)
	dev.host.Token = "wrong"
	noToken := config.Host{Name: "git.internal.test", APIHost: "127.0.0.1:1", APIProtocol: "http"}
	unusedGlab := config.Host{Name: "gitlab.unused.test", APIHost: "127.0.0.1:1", APIProtocol: "http", FromGlab: true}
	st, err := NewState([]config.Host{com.host, dev.host, noToken, unusedGlab}, config.DefaultSections[:1])
	if err != nil {
		t.Fatal(err)
	}
	st.Sections[0].Limit = 20
	RunAll(context.Background(), st)

	if e := st.Hosts[1].Err; e == nil || !strings.Contains(e.Error(), "token rejected (401)") || !strings.Contains(e.Error(), "GL_DASH_TOKEN_GITLAB_EXAMPLE_DEV") {
		t.Errorf("dev err = %v", e)
	}
	if e := st.Hosts[2].Err; e == nil || !strings.Contains(e.Error(), "no token") || !strings.Contains(e.Error(), "glab auth login --hostname git.internal.test") {
		t.Errorf("no-token err = %v", e)
	}
	if h := st.Hosts[3]; h.Err != nil || !h.Skipped {
		t.Errorf("glab host without token: err=%v skipped=%v, want quietly skipped", h.Err, h.Skipped)
	}
	if st.Busy() != 0 || st.Sections[0].Loading != 0 {
		t.Errorf("busy=%d loading=%d, want 0", st.Busy(), st.Sections[0].Loading)
	}
	if n := len(st.Sections[0].Rows); n != 1 {
		t.Errorf("rows = %d, want 1 from gitlab.com", n)
	}
	if dev.srv.Requests() != 1 {
		t.Errorf("dev requests = %d, want 1 (stop after 401)", dev.srv.Requests())
	}
}

func TestSectionLimitAfterMerge(t *testing.T) {
	now := time.Now()
	com, dev := startHost(t, comDataset(), now), startHost(t, devDataset(), now)
	st, _ := NewState([]config.Host{com.host, dev.host}, []config.Section{{Title: "All", Filters: "state:opened", Limit: 3}})
	RunAll(context.Background(), st)
	// fake API returns per host; the merged section must still hold 3.
	if n := len(st.Sections[0].Rows); n != 3 {
		t.Errorf("rows = %d, want 3", n)
	}
}

func TestParseFilter(t *testing.T) {
	f, err := ParseFilter("reviewer:@me draft:false label:backend label:urgent my-review-state:unreviewed")
	if err != nil {
		t.Fatal(err)
	}
	q := f.Query("alex", 20)
	for k, v := range map[string]string{"reviewer_username": "alex", "draft": "false", "labels": "backend,urgent", "state": "opened", "scope": "all", "per_page": "20"} {
		if q.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, q.Get(k), v)
		}
	}
	for _, bad := range []string{"reviewer", "colour:red", "draft:maybe", "review-state:lgtm", "state:open"} {
		if _, err := ParseFilter(bad); err == nil {
			t.Errorf("ParseFilter(%q): want error", bad)
		}
	}
}

func TestKeep(t *testing.T) {
	revs := []reviewer{{"me", "unreviewed"}, {"sam", "requested_changes"}}
	cases := []struct {
		filter string
		want   bool
	}{
		{"my-review-state:unreviewed", true},
		{"my-review-state:approved", false},
		{"review-state:requested_changes", true},
		{"review-state:reviewed", false},
		// Your own state never counts as "any other reviewer".
		{"review-state:unreviewed", false},
	}
	for _, c := range cases {
		f, _ := ParseFilter(c.filter)
		if got := f.keep("me", revs); got != c.want {
			t.Errorf("%s: keep = %v, want %v", c.filter, got, c.want)
		}
	}
}

func TestReviewStateFilterReadsPastFirstPage(t *testing.T) {
	ds := fakegitlab.Dataset{Host: "gitlab.com", Token: "t", User: "alex"}
	for i := 1; i <= 5; i++ {
		ds.MRs = append(ds.MRs, fakegitlab.MR{
			ProjectID: 1, Project: "acme/shop", IID: i, Title: "approved " + strconv.Itoa(i), Author: "sam",
			Reviewers: []fakegitlab.Reviewer{{Username: "alex", State: "approved"}},
			Updated:   ago(time.Duration(i) * time.Hour),
		})
	}
	for i := 6; i <= 8; i++ {
		ds.MRs = append(ds.MRs, fakegitlab.MR{
			ProjectID: 1, Project: "acme/shop", IID: i, Title: "waiting " + strconv.Itoa(i), Author: "sam",
			Reviewers: []fakegitlab.Reviewer{{Username: "alex", State: "unreviewed"}},
			Updated:   ago(time.Duration(i) * time.Hour),
		})
	}
	h := startHost(t, ds, time.Now())
	src, err := Connect(t.Context(), h.host)
	if err != nil {
		t.Fatal(err)
	}
	f, _ := ParseFilter("reviewer:@me my-review-state:unreviewed")
	rows, err := src.Section(t.Context(), f, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []int
	for _, r := range rows {
		got = append(got, r.IID)
	}
	if len(got) != 2 || got[0] != 6 || got[1] != 7 {
		t.Errorf("rows = %v, want [6 7]", got)
	}
}

func TestDetailsOnlyForRowsKeptAfterMerge(t *testing.T) {
	st, _ := NewState(nil, []config.Section{{Title: "All", Filters: "state:opened", Limit: 1}})
	now := time.Now()
	newer := Row{Key: Key{Host: "a", ProjectID: 1, IID: 1}, Updated: now}
	older := Row{Key: Key{Host: "b", ProjectID: 1, IID: 2}, Updated: now.Add(-time.Hour)}
	st.Apply(listed{section: 0, rows: []Row{newer}})
	if jobs := st.Apply(listed{section: 0, rows: []Row{older}}); len(jobs) != 0 {
		t.Errorf("queued %d detail jobs for a row trimmed by the section limit", len(jobs))
	}
}

// pagedHost serves ds and counts /merge_requests requests. When stuck is
// set, every list request returns the first page, as if each page request
// raced an update that shifted the list.
func pagedHost(t *testing.T, ds fakegitlab.Dataset, stuck bool) (*Source, *atomic.Int64) {
	t.Helper()
	fake := fakegitlab.New(ds, time.Now(), 0)
	var lists atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v4/merge_requests" {
			lists.Add(1)
			if stuck {
				q := r.URL.Query()
				q.Set("page", "1")
				r.URL.RawQuery = q.Encode()
			}
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	src, err := Connect(t.Context(), config.Host{Name: ds.Host, APIHost: strings.TrimPrefix(ts.URL, "http://"), APIProtocol: "http", Token: ds.Token})
	if err != nil {
		t.Fatal(err)
	}
	return src, &lists
}

func reviewQueue(approved, waiting int) fakegitlab.Dataset {
	ds := fakegitlab.Dataset{Host: "gitlab.com", Token: "t", User: "alex"}
	for i := 1; i <= approved+waiting; i++ {
		state := "approved"
		if i > approved {
			state = "unreviewed"
		}
		ds.MRs = append(ds.MRs, fakegitlab.MR{
			ProjectID: 1, Project: "acme/shop", IID: i, Title: state, Author: "sam",
			Reviewers: []fakegitlab.Reviewer{{Username: "alex", State: state}},
			Updated:   ago(time.Duration(i) * time.Hour),
		})
	}
	return ds
}

func TestReviewStatePagingStopsAtCap(t *testing.T) {
	src, lists := pagedHost(t, reviewQueue(20, 1), false)
	f, _ := ParseFilter("reviewer:@me my-review-state:unreviewed")
	rows, err := src.Section(t.Context(), f, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("rows = %v, want none within the first 5 pages", rows)
	}
	if n := lists.Load(); n != 5 {
		t.Errorf("list requests = %d, want 5", n)
	}
}

func TestPagingSkipsRepeatedRows(t *testing.T) {
	ds := reviewQueue(0, 6)
	for i := 1; i < len(ds.MRs); i++ {
		ds.MRs[i].Reviewers[0].State = "approved"
	}
	src, lists := pagedHost(t, ds, true)
	f, _ := ParseFilter("reviewer:@me my-review-state:unreviewed")
	rows, err := src.Section(t.Context(), f, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].IID != 1 {
		t.Errorf("rows = %v, want MR 1 once", rows)
	}
	if n := lists.Load(); n != 5 {
		t.Errorf("list requests = %d, want 5", n)
	}
}
