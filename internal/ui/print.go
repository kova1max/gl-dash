package ui

import (
	"fmt"
	"io"
	"time"

	"github.com/kova1max/gl-dash/internal/dash"
)

// Print writes every section of a fully loaded state as plain text.
func Print(w io.Writer, st *dash.State, now time.Time, width int) {
	p := newPalette(false)
	fmt.Fprintln(w, authLine(st))
	for _, h := range st.Hosts {
		if h.Err != nil {
			fmt.Fprintln(w, "error:", h.Err)
		}
	}
	var all []dash.Row
	for _, sec := range st.Sections {
		all = append(all, sec.Rows...)
	}
	l := newLayout(width, all)
	for _, sec := range st.Sections {
		fmt.Fprintf(w, "\n%s (%d)\n", sec.Title, len(sec.Rows))
		for _, err := range sec.Errs {
			fmt.Fprintln(w, "error:", err)
		}
		if len(sec.Rows) == 0 {
			continue
		}
		fmt.Fprintln(w, l.header(p))
		for _, r := range sec.Rows {
			for _, line := range l.lines(p, viewOf(st, r), false, now, width) {
				fmt.Fprintln(w, line)
			}
		}
	}
}
