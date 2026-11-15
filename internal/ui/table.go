// Package ui renders the dashboard, as a Bubble Tea program or as plain text.
package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kova1max/gl-dash/internal/dash"
)

// palette holds the styles; the zero value renders plain text.
type palette struct {
	styled                                               bool
	red, green, yellow, dim, accent, bold, tabOn, tabOff lipgloss.Style
}

func newPalette(styled bool) palette {
	if !styled {
		return palette{}
	}
	s := lipgloss.NewStyle
	return palette{
		styled: true,
		red:    s().Foreground(lipgloss.Color("1")),
		green:  s().Foreground(lipgloss.Color("2")),
		yellow: s().Foreground(lipgloss.Color("3")),
		dim:    s().Foreground(lipgloss.Color("8")),
		accent: s().Foreground(lipgloss.Color("5")).Bold(true),
		bold:   s().Bold(true),
		tabOn:  s().Foreground(lipgloss.Color("5")).Bold(true).Underline(true),
		tabOff: s().Foreground(lipgloss.Color("8")),
	}
}

func (p palette) paint(st lipgloss.Style, s string) string {
	if !p.styled || s == "" {
		return s
	}
	return st.Render(s)
}

// column widths for one render width.
type layout struct {
	project, title, author, host int
}

const (
	wCI, wAppr, wThr, wConfl, wAge = 2, 5, 3, 5, 4
	marker                         = 2 // selection marker column
)

func newLayout(width int, rows []dash.Row) layout {
	l := layout{author: 6, host: 4}
	maxProject := 7
	for _, r := range rows {
		l.host = max(l.host, len(r.Host))
		l.author = max(l.author, len(r.Author))
		maxProject = max(maxProject, ansi.StringWidth(r.Project))
	}
	l.host, l.author = min(l.host, 22), min(l.author, 12)
	fixed := marker + l.author + wCI + wAppr + wThr + wConfl + wAge + l.host + 8 // 8 gaps after project..age
	rest := max(width-fixed-1, 30)
	l.project = min(max(rest*2/5, 12), maxProject)
	l.title = rest - l.project
	return l
}

func cell(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return s + strings.Repeat(" ", max(w-ansi.StringWidth(s), 0))
}

func rcell(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	return strings.Repeat(" ", max(w-ansi.StringWidth(s), 0)) + s
}

func (l layout) header(p palette) string {
	h := strings.Repeat(" ", marker) + strings.Join([]string{
		cell("PROJECT", l.project), cell("TITLE", l.title), cell("AUTHOR", l.author),
		cell("CI", wCI), rcell("APPR", wAppr), rcell("THR", wThr), cell("CONFL", wConfl),
		rcell("UPD", wAge), cell("HOST", l.host),
	}, " ")
	return p.paint(p.dim, strings.TrimRight(h, " "))
}

// rowView is a row with everything known about it so far.
type rowView struct {
	dash.Row
	detail     *dash.Detail
	detailErr  error
	blocker    *dash.Blocker
	blockerErr error
}

func viewOf(st *dash.State, r dash.Row) rowView {
	v := rowView{Row: r, detailErr: st.DetailErr[r.Key], blockerErr: st.BlockerErr[r.Key]}
	if d, ok := st.Details[r.Key]; ok {
		v.detail = &d
	}
	if b, ok := st.Blockers[r.Key]; ok {
		v.blocker = &b
	}
	return v
}

// lines renders a row as one line, plus a second line explaining a failed
// pipeline.
func (l layout) lines(p palette, v rowView, selected bool, now time.Time, width int) []string {
	mark := "  "
	title := cell(v.Title, l.title)
	if selected {
		mark = p.paint(p.accent, "▍ ")
		if !p.styled {
			mark = "> "
		}
		title = p.paint(p.bold, title)
	}
	ci, appr, thr := p.paint(p.dim, "·"), p.paint(p.dim, rcell("·", wAppr)), p.paint(p.dim, rcell("·", wThr))
	if v.detailErr != nil {
		ci, appr, thr = p.paint(p.red, "!"), p.paint(p.red, rcell("!", wAppr)), p.paint(p.red, rcell("!", wThr))
	} else if d := v.detail; d != nil {
		ci = pipelineIcon(p, d.Pipeline)
		appr = approvals(p, d)
		thr = rcell(fmt.Sprint(d.Unresolved), wThr)
		if d.Unresolved > 0 {
			thr = p.paint(p.yellow, thr)
		} else {
			thr = p.paint(p.dim, thr)
		}
	}
	confl := cell("", wConfl)
	if v.Conflicts {
		confl = p.paint(p.red, cell("yes", wConfl))
	}
	line := mark + strings.Join([]string{
		cell(v.Project, l.project), title, p.paint(p.dim, cell(v.Author, l.author)),
		cell(ci, wCI), appr, thr, confl,
		p.paint(p.dim, rcell(age(now.Sub(v.Updated)), wAge)), p.paint(p.dim, cell(v.Host, l.host)),
	}, " ")
	out := []string{strings.TrimRight(line, " ")}
	if b := blockerText(v); b != "" {
		b = ansi.Truncate("    ✗ "+b, max(width-1, 10), "…")
		if v.blocker == nil {
			out = append(out, p.paint(p.dim, b))
		} else {
			out = append(out, p.paint(p.red, b))
		}
	}
	return out
}

// blockerText explains a failed pipeline, or returns "" when it passed.
func blockerText(v rowView) string {
	if v.detail == nil || v.detail.Pipeline != "failed" {
		return ""
	}
	b := v.blocker
	switch {
	case b == nil:
		return "pipeline failed · reading job log…"
	case b.Job == "":
		return "pipeline failed · no failed job in this pipeline (child pipelines are not followed yet)"
	case v.blockerErr != nil:
		return b.Job + " · could not read job log: " + v.blockerErr.Error()
	case b.Line == "":
		return b.Job + " failed"
	default:
		return b.Job + " · " + b.Line
	}
}

func pipelineIcon(p palette, status string) string {
	switch status {
	case "success":
		return p.paint(p.green, "✓")
	case "failed":
		return p.paint(p.red, "✗")
	case "running":
		return p.paint(p.yellow, "●")
	case "":
		return p.paint(p.dim, "-")
	case "canceled", "skipped":
		return p.paint(p.dim, "⊘")
	default: // created, pending, preparing, scheduled, manual, waiting_for_resource
		return p.paint(p.dim, "○")
	}
}

func approvals(p palette, d *dash.Detail) string {
	switch {
	case d.ApprovalsRequired > 0:
		s := rcell(fmt.Sprintf("%d/%d", d.ApprovalsGiven, d.ApprovalsRequired), wAppr)
		if d.ApprovalsGiven >= d.ApprovalsRequired {
			return p.paint(p.green, s)
		}
		return s
	case d.ApprovalsGiven > 0:
		return p.paint(p.green, rcell(fmt.Sprint(d.ApprovalsGiven), wAppr))
	default:
		return p.paint(p.dim, rcell("-", wAppr))
	}
}

func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", max(int(d.Minutes()), 0))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dw", int(d.Hours()/24/7))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/24/365))
	}
}

// searchText is what the / filter matches against.
func searchText(v rowView) string {
	parts := []string{v.Project, v.Title, v.Author, v.Host, fmt.Sprintf("!%d", v.IID)}
	if v.Draft {
		parts = append(parts, "draft")
	}
	if v.Conflicts {
		parts = append(parts, "conflict")
	}
	if v.detail != nil {
		parts = append(parts, v.detail.Pipeline)
	}
	parts = append(parts, blockerText(v))
	return strings.ToLower(strings.Join(parts, " "))
}

func matches(v rowView, filter string) bool {
	text := searchText(v)
	for _, term := range strings.Fields(strings.ToLower(filter)) {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}

// authLine summarises where each host's token came from.
func authLine(st *dash.State) string {
	var order []string
	bySource := map[string][]string{}
	for _, h := range st.Hosts {
		src := h.Host.TokenSource
		if src == "" || (h.Done && h.Err != nil) {
			continue
		}
		if _, ok := bySource[src]; !ok {
			order = append(order, src)
		}
		bySource[src] = append(bySource[src], h.Host.Name)
	}
	var parts []string
	for _, src := range order {
		parts = append(parts, fmt.Sprintf("%s (%s)", src, strings.Join(bySource[src], ", ")))
	}
	if len(parts) == 0 {
		return "auth: no working token"
	}
	return "auth: " + strings.Join(parts, " · ")
}
