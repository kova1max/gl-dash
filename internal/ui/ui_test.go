package ui

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kova1max/gl-dash/internal/config"
	"github.com/kova1max/gl-dash/internal/dash"
	"github.com/kova1max/gl-dash/internal/fakegitlab"
)

var now = time.Date(2026, 11, 15, 12, 0, 0, 0, time.UTC)

// demoHosts serves the demo datasets, so these tests also check what the
// README recording shows.
func demoHosts(t *testing.T) []config.Host {
	t.Helper()
	var hosts []config.Host
	for _, f := range []string{"gitlab.com.json", "gitlab.example.dev.json"} {
		ds, err := fakegitlab.Load("../../demo/data/" + f)
		if err != nil {
			t.Fatal(err)
		}
		ts := httptest.NewServer(fakegitlab.New(ds, now, 0))
		t.Cleanup(ts.Close)
		hosts = append(hosts, config.Host{
			Name: ds.Host, APIHost: strings.TrimPrefix(ts.URL, "http://"), APIProtocol: "http",
			Token: ds.Token, TokenSource: "glab",
		})
	}
	return hosts
}

// sections returns the default sections with defaults applied.
func sections(t *testing.T) []config.Section {
	t.Helper()
	f, err := config.Load(t.TempDir()+"/none.yml", false)
	if err != nil {
		t.Fatal(err)
	}
	return f.MRSections
}

// drain runs a command and every command it leads to, feeding results back
// into the model, like Bubble Tea does.
func drain(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			drain(t, m, c)
		}
	case nil:
	default:
		_, next := m.Update(msg)
		drain(t, m, next)
	}
}

func press(t *testing.T, m *Model, keys ...string) {
	t.Helper()
	for _, k := range keys {
		var msg tea.KeyPressMsg
		switch k {
		case "enter":
			msg = tea.KeyPressMsg{Code: tea.KeyEnter}
		case "esc":
			msg = tea.KeyPressMsg{Code: tea.KeyEscape}
		case "tab":
			msg = tea.KeyPressMsg{Code: tea.KeyTab}
		default:
			r := []rune(k)[0]
			msg = tea.KeyPressMsg{Code: r, Text: k}
		}
		_, cmd := m.Update(msg)
		drain(t, m, cmd)
	}
}

func newLoaded(t *testing.T, width, height int) (*Model, *[]string) {
	t.Helper()
	var opened []string
	m, err := New(demoHosts(t), sections(t), func() time.Time { return now },
		func(u string) error { opened = append(opened, u); return nil })
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: width, Height: height})
	drain(t, m, m.Init())
	return m, &opened
}

func screen(m *Model) string { return ansi.Strip(m.render()) }

func TestLoadsBothHostsWithErrorLines(t *testing.T) {
	m, _ := newLoaded(t, 110, 28)
	s := screen(m)
	for _, want := range []string{
		"Review requested (7)", "Returned to you (2)", "Your merge requests (4)",
		"auth: glab (gitlab.com, gitlab.example.dev)",
		"✗ test:unit · expected 200, got 500 (orders_test.go:88)",
		"✗ lint · declared and not used: ctx (handler.go:42)",
		"gitlab.example.dev",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("screen missing %q:\n%s", want, s)
		}
	}
	if m.st.Busy() != 0 {
		t.Errorf("busy = %d after drain", m.st.Busy())
	}
	if lines := strings.Count(s, "\n") + 1; lines != 28 {
		t.Errorf("screen has %d lines, want 28", lines)
	}
}

func TestSectionsFilterAndOpen(t *testing.T) {
	m, opened := newLoaded(t, 110, 28)

	press(t, m, "tab")
	if s := screen(m); !strings.Contains(s, "Dark mode for the account pages") || strings.Contains(s, "Retry failed webhook") {
		t.Fatalf("tab did not switch to Returned to you:\n%s", s)
	}

	press(t, m, "l", "/", "c", "o", "n", "f", "l", "i", "c", "t", "enter")
	if rows := m.visible(); len(rows) != 1 || rows[0].Title != "Dark mode for the account pages" {
		t.Fatalf("filter conflict = %v", rows)
	}
	press(t, m, "o")
	if len(*opened) != 1 || (*opened)[0] != "https://gitlab.com/acme/web/-/merge_requests/90" {
		t.Errorf("opened = %v", *opened)
	}

	press(t, m, "esc")
	if n := len(m.visible()); n != 4 {
		t.Errorf("after esc rows = %d, want 4", n)
	}

	// Error text is searchable too.
	press(t, m, "h", "h", "/", "o", "r", "d", "e", "r", "s", "_", "t", "e", "s", "t", "enter")
	if rows := m.visible(); len(rows) != 1 || rows[0].IID != 214 {
		t.Errorf("filter on error line = %v", rows)
	}
}

func TestScrollKeepsCursorVisible(t *testing.T) {
	m, _ := newLoaded(t, 110, 9) // room for 5 list lines
	press(t, m, "G")
	s := screen(m)
	if !strings.Contains(s, "▍ data/etl-jobs") {
		t.Errorf("last row not visible after G:\n%s", s)
	}
	press(t, m, "g")
	if s := screen(m); !strings.Contains(s, "▍ acme/shop-api") {
		t.Errorf("first row not visible after g:\n%s", s)
	}
}

func TestRefreshDropsStaleResults(t *testing.T) {
	m, _ := newLoaded(t, 110, 28)
	stale := genEvent{gen: m.gen, ev: nil}
	_, cmd := m.Update(tea.KeyPressMsg{Code: 'r', Text: "r"})
	if m.st.Busy() == 0 {
		t.Fatal("refresh started no jobs")
	}
	// A result from the previous load must not touch the new state.
	m.Update(stale)
	drain(t, m, cmd)
	if got := len(m.st.Sections[0].Rows); got != 7 {
		t.Errorf("rows after refresh = %d, want 7", got)
	}
}

func TestHostErrorShownWithFix(t *testing.T) {
	hosts := demoHosts(t)
	hosts[1].Token = "expired"
	m, err := New(hosts, sections(t), func() time.Time { return now }, nil)
	if err != nil {
		t.Fatal(err)
	}
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 28})
	drain(t, m, m.Init())
	s := screen(m)
	if !strings.Contains(s, "✗ gitlab.example.dev: token rejected (401); run `glab auth login --hostname gitlab.example.dev` or set GL_DASH_TOKEN_GITLAB_EXAMPLE_DEV") {
		t.Errorf("missing host error:\n%s", s)
	}
	if !strings.Contains(s, "Review requested (4)") {
		t.Errorf("gitlab.com rows missing:\n%s", s)
	}
}

func TestPrint(t *testing.T) {
	st, err := dash.NewState(demoHosts(t), sections(t))
	if err != nil {
		t.Fatal(err)
	}
	dash.RunAll(t.Context(), st)
	var b bytes.Buffer
	Print(&b, st, now, 110)
	out := b.String()
	if strings.Contains(out, "\x1b[") {
		t.Error("print output contains ANSI escapes")
	}
	for _, want := range []string{
		"Review requested (7)",
		"  acme/shop-api     Add idempotency keys to order creation  sam    ✗    1/2   2        25m gitlab.com",
		"    ✗ test:py · assert 25 == 30",
		"  acme/web          Dark mode for the account pages         alex   ✓    0/1   4 yes    40m gitlab.com",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("print missing %q:\n%s", want, out)
		}
	}
}

func TestBlockerJobListErrorShown(t *testing.T) {
	hosts := demoHosts(t)[:1]
	ds, err := fakegitlab.Load("../../demo/data/gitlab.com.json")
	if err != nil {
		t.Fatal(err)
	}
	fake := fakegitlab.New(ds, now, 0)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/jobs") {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		fake.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	hosts[0].APIHost = strings.TrimPrefix(ts.URL, "http://")
	st, err := dash.NewState(hosts, sections(t))
	if err != nil {
		t.Fatal(err)
	}
	dash.RunAll(t.Context(), st)
	var b bytes.Buffer
	Print(&b, st, now, 200)
	out := b.String()
	if !strings.Contains(out, "✗ pipeline failed · could not list jobs: GET /projects/") || !strings.Contains(out, "500 Internal Server Error") {
		t.Errorf("print missing job list error:\n%s", out)
	}
	if strings.Contains(out, "no failed job in this pipeline") {
		t.Errorf("job list error reported as no failed job:\n%s", out)
	}
}

func TestAge(t *testing.T) {
	for d, want := range map[time.Duration]string{
		-time.Minute:                  "0m",
		59 * time.Minute:              "59m",
		5 * time.Hour:                 "5h",
		3 * 24 * time.Hour:            "3d",
		30 * 24 * time.Hour:           "4w",
		400 * 24 * time.Hour:          "1y",
		13*24*time.Hour + 1:           "13d",
		14 * 24 * time.Hour:           "2w",
		364 * 24 * time.Hour:          "52w",
		730 * 24 * time.Hour:          "2y",
		90 * time.Second:              "1m",
		23*time.Hour + 59*time.Minute: "23h",
	} {
		if got := age(d); got != want {
			t.Errorf("age(%v) = %q, want %q", d, got, want)
		}
	}
}
