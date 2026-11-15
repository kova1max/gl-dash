package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kova1max/gl-dash/internal/fakegitlab"
)

func newFake(t *testing.T, ds fakegitlab.Dataset) (*Client, *fakegitlab.Server) {
	t.Helper()
	srv := fakegitlab.New(ds, time.Now(), 0)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return New("http", strings.TrimPrefix(ts.URL, "http://"), ds.Token), srv
}

func TestUnresolvedThreadsPaginates(t *testing.T) {
	c, srv := newFake(t, fakegitlab.Dataset{Token: "t", User: "me", MRs: []fakegitlab.MR{
		{ProjectID: 1, IID: 2, Author: "me", Unresolved: 130, Resolved: 40},
	}})
	n, err := c.UnresolvedThreads(context.Background(), 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 171 threads (one plain comment) over two pages of 100.
	if n != 130 || srv.Requests() != 2 {
		t.Errorf("unresolved = %d in %d requests, want 130 in 2", n, srv.Requests())
	}
}

func TestUnauthorized(t *testing.T) {
	c, _ := newFake(t, fakegitlab.Dataset{Token: "right", User: "me"})
	c.token = "wrong"
	_, err := c.CurrentUser(context.Background())
	if !IsUnauthorized(err) {
		t.Errorf("err = %v, want 401", err)
	}
}

func TestOnlyGETWithBearer(t *testing.T) {
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.Header.Get("Authorization"))
		w.Write([]byte(`{"id":1,"username":"me"}`))
	}))
	defer ts.Close()
	c := New("http", strings.TrimPrefix(ts.URL, "http://"), "tok")
	if _, err := c.CurrentUser(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || seen[0] != "GET Bearer tok" {
		t.Errorf("requests = %v", seen)
	}
}

func TestJobTraceIsCapped(t *testing.T) {
	big := strings.Repeat("x", maxTraceBytes+1000)
	c, _ := newFake(t, fakegitlab.Dataset{Token: "t", User: "me", MRs: []fakegitlab.MR{
		{ProjectID: 1, IID: 2, Pipeline: &fakegitlab.Pipeline{ID: 3, Status: "failed", Jobs: []fakegitlab.Job{{ID: 4, Status: "failed", Trace: big}}}},
	}})
	tr, err := c.JobTrace(context.Background(), 1, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr) != maxTraceBytes {
		t.Errorf("trace length = %d, want %d", len(tr), maxTraceBytes)
	}
}
