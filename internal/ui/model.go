package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kova1max/gl-dash/internal/config"
	"github.com/kova1max/gl-dash/internal/dash"
)

// Model is the Bubble Tea model of the dashboard.
type Model struct {
	hosts    []config.Host
	sections []config.Section
	now      func() time.Time
	openURL  func(string) error

	st     *dash.State
	gen    int
	ctx    context.Context
	cancel context.CancelFunc
	loaded time.Time

	sec     int
	cursor  []int
	offset  []int
	filter  string
	editing bool
	help    bool
	flash   string

	width, height int
}

// New returns a dashboard model. openURL opens a merge request in the browser.
func New(hosts []config.Host, sections []config.Section, now func() time.Time, openURL func(string) error) (*Model, error) {
	m := &Model{hosts: hosts, sections: sections, now: now, openURL: openURL, width: 100, height: 30}
	m.cursor = make([]int, len(sections))
	m.offset = make([]int, len(sections))
	if _, err := dash.NewState(hosts, sections); err != nil {
		return nil, err
	}
	return m, nil
}

// genEvent tags an event with the refresh generation that started it, so
// results of a cancelled load are dropped.
type genEvent struct {
	gen int
	ev  dash.Event
}

type openedMsg struct{ err error }

// Init starts loading.
func (m *Model) Init() tea.Cmd { return m.reload() }

func (m *Model) reload() tea.Cmd {
	if m.cancel != nil {
		m.cancel()
	}
	st, _ := dash.NewState(m.hosts, m.sections) // validated in New
	m.st = st
	m.gen++
	m.ctx, m.cancel = context.WithCancel(context.Background())
	return m.run(st.Start())
}

func (m *Model) run(jobs []dash.Job) tea.Cmd {
	gen, ctx := m.gen, m.ctx
	var cmds []tea.Cmd
	for _, j := range jobs {
		cmds = append(cmds, func() tea.Msg { return genEvent{gen, j(ctx)} })
	}
	return tea.Batch(cmds...)
}

// Update handles messages.
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case genEvent:
		if msg.gen != m.gen {
			return m, nil
		}
		jobs := m.st.Apply(msg.ev)
		if m.st.Busy() == 0 {
			m.loaded = m.now()
		}
		m.clamp()
		return m, m.run(jobs)
	case openedMsg:
		if msg.err != nil {
			m.flash = "could not open browser: " + msg.err.Error()
		}
	case tea.KeyPressMsg:
		return m, m.key(msg.String())
	}
	return m, nil
}

func (m *Model) key(k string) tea.Cmd {
	if m.editing {
		switch k {
		case "enter":
			m.editing = false
		case "esc":
			m.editing, m.filter = false, ""
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
		case "space":
			m.filter += " "
		case "ctrl+c":
			return tea.Quit
		default:
			if len([]rune(k)) == 1 {
				m.filter += k
			}
		}
		m.cursor[m.sec], m.offset[m.sec] = 0, 0
		return nil
	}
	m.flash = ""
	if m.help && k != "ctrl+c" && k != "q" {
		m.help = false
		return nil
	}
	n := len(m.visible())
	switch k {
	case "q", "ctrl+c":
		if m.cancel != nil {
			m.cancel()
		}
		return tea.Quit
	case "?":
		m.help = true
	case "j", "down":
		m.cursor[m.sec]++
	case "k", "up":
		m.cursor[m.sec]--
	case "g", "home":
		m.cursor[m.sec] = 0
	case "G", "end":
		m.cursor[m.sec] = n - 1
	case "ctrl+d", "pgdown":
		m.cursor[m.sec] += max(m.listHeight()/2, 1)
	case "ctrl+u", "pgup":
		m.cursor[m.sec] -= max(m.listHeight()/2, 1)
	case "l", "right", "tab":
		m.sec = (m.sec + 1) % len(m.sections)
	case "h", "left", "shift+tab":
		m.sec = (m.sec + len(m.sections) - 1) % len(m.sections)
	case "/":
		m.editing = true
	case "esc":
		m.filter = ""
	case "r":
		return m.reload()
	case "o", "enter":
		if rows := m.visible(); len(rows) > 0 {
			url := rows[m.cursor[m.sec]].WebURL
			return func() tea.Msg { return openedMsg{m.openURL(url)} }
		}
	}
	m.clamp()
	return nil
}

// visible returns the current section's rows that match the filter.
func (m *Model) visible() []rowView {
	var out []rowView
	for _, r := range m.st.Sections[m.sec].Rows {
		v := viewOf(m.st, r)
		if m.filter == "" || matches(v, m.filter) {
			out = append(out, v)
		}
	}
	return out
}

func (m *Model) clamp() {
	n := len(m.visible())
	m.cursor[m.sec] = max(min(m.cursor[m.sec], n-1), 0)
}

// chrome is the number of lines around the list: tabs, status, header, footer
// and one line per host error.
func (m *Model) chrome() int {
	n := 4
	for _, h := range m.st.Hosts {
		if h.Err != nil {
			n++
		}
	}
	return n + len(m.st.Sections[m.sec].Errs)
}

func (m *Model) listHeight() int { return max(m.height-m.chrome(), 1) }

// View renders the dashboard.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.WindowTitle = "gl-dash"
	return v
}

func (m *Model) render() string {
	p := newPalette(true)
	var b strings.Builder
	line := func(s string) {
		b.WriteString(ansi.Truncate(s, m.width, "…"))
		b.WriteByte('\n')
	}

	// Tabs.
	var tabs []string
	for i, s := range m.st.Sections {
		label := fmt.Sprintf("%s (%d)", s.Title, len(s.Rows))
		if s.Loading > 0 && len(s.Rows) == 0 {
			label = s.Title + " (…)"
		}
		if i == m.sec {
			tabs = append(tabs, p.paint(p.tabOn, label))
		} else {
			tabs = append(tabs, p.paint(p.tabOff, label))
		}
	}
	line(" " + strings.Join(tabs, p.paint(p.dim, "  │  ")))

	// Status.
	status := authLine(m.st)
	if busy := m.st.Busy(); busy > 0 {
		status += p.paint(p.dim, fmt.Sprintf("  ·  loading (%d)", busy))
	} else if !m.loaded.IsZero() {
		status += p.paint(p.dim, "  ·  updated "+m.loaded.Format("15:04"))
	}
	line(" " + p.paint(p.dim, status))
	for _, h := range m.st.Hosts {
		if h.Err != nil {
			line(" " + p.paint(p.red, "✗ "+h.Err.Error()))
		}
	}
	for _, err := range m.st.Sections[m.sec].Errs {
		line(" " + p.paint(p.red, "✗ "+err.Error()))
	}

	if m.help {
		b.WriteString(helpText)
		return b.String()
	}

	var all []dash.Row
	for _, s := range m.st.Sections {
		all = append(all, s.Rows...)
	}
	l := newLayout(m.width, all)
	line(l.header(p))

	rows := m.visible()
	listH := m.listHeight()
	body := m.body(p, l, rows, listH)
	for _, s := range body {
		line(s)
	}
	for i := len(body); i < listH; i++ {
		b.WriteByte('\n')
	}
	line(m.footer(p, len(rows)))
	return strings.TrimSuffix(b.String(), "\n")
}

// body renders the rows that fit in h lines, scrolled to keep the cursor visible.
func (m *Model) body(p palette, l layout, rows []rowView, h int) []string {
	sec := m.st.Sections[m.sec]
	if len(rows) == 0 {
		switch {
		case sec.Loading > 0:
			return []string{p.paint(p.dim, "  loading…")}
		case m.filter != "":
			return []string{p.paint(p.dim, "  No merge requests match \""+m.filter+"\".")}
		default:
			return []string{p.paint(p.dim, "  Nothing here.")}
		}
	}
	now := m.now()
	rendered := make([][]string, len(rows))
	for i, r := range rows {
		rendered[i] = l.lines(p, r, i == m.cursor[m.sec], now, m.width)
	}
	cur := m.cursor[m.sec]
	off := min(m.offset[m.sec], cur)
	for {
		used := 0
		for i := off; i <= cur; i++ {
			used += len(rendered[i])
		}
		if used <= h || off == cur {
			break
		}
		off++
	}
	m.offset[m.sec] = off
	var out []string
	for i := off; i < len(rendered) && len(out)+len(rendered[i]) <= h; i++ {
		out = append(out, rendered[i]...)
	}
	return out
}

func (m *Model) footer(p palette, n int) string {
	if m.editing {
		return " /" + m.filter + "▏"
	}
	if m.flash != "" {
		return " " + p.paint(p.red, m.flash)
	}
	left := " j/k move · h/l section · / filter · o open · r refresh · ? help · q quit"
	if m.filter != "" {
		left = fmt.Sprintf(" filter: %q (%d) · esc clear ·", m.filter, n) + left
	}
	return p.paint(p.dim, left)
}

const helpText = `
  Keys
    j, ↓          next merge request
    k, ↑          previous merge request
    g, G          first, last
    ctrl+d/u      half page down, up
    l, →, tab     next section
    h, ←, ⇧tab    previous section
    /             filter rows (matches project, title, author, host,
                  "draft", "conflict", pipeline status and error text)
    esc           clear filter
    o, enter      open merge request in browser
    r             refresh
    q             quit

  Press any key to close.
`
