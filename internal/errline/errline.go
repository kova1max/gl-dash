// Package errline finds the first meaningful error line in a GitLab CI job log.
//
// It is deliberately conservative: a wrong line is worse than no line, so it
// only reports lines that match a known error shape. When nothing matches it
// falls back to the runner's own "Job failed" reason.
package errline

import (
	"path"
	"regexp"
	"strings"
)

var (
	ansiRe    = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	sectionRe = regexp.MustCompile(`^section_(start|end):\d+:([A-Za-z0-9_.-]+)`)

	// Runner boilerplate that looks like an error but is not the cause.
	runnerFailRe = regexp.MustCompile(`^ERROR: Job failed(?: \([^)]*\))?: (.+)$`)

	// file:line[:col]: message, e.g. compiler, go vet and linter output.
	locRe = regexp.MustCompile(`^(?:\./)?([\w./@+-]+\.[A-Za-z0-9]+):(\d+)(?::\d+)?: (.+)$`)
	// Trailing linter name, e.g. "(errcheck)" from golangci-lint.
	linterRe = regexp.MustCompile(` \([a-z0-9-]+\)$`)
	// go test failure detail, indented under the test.
	goTestLineRe = regexp.MustCompile(`^\s+([\w.-]+_test\.go):(\d+): (.+)$`)
	goRunRe      = regexp.MustCompile(`^=== RUN\s+(\S+)`)
	goFailRe     = regexp.MustCompile(`^\s*--- FAIL: (\S+)`)
)

// rule matches an error line. When group is set, the first capture group is
// the message; otherwise the whole line is.
type rule struct {
	re    *regexp.Regexp
	group bool
}

var rules = []rule{
	{regexp.MustCompile(`^panic: .+`), false},
	{regexp.MustCompile(`^E\s{2,}(.+)$`), true},                                // pytest assertion detail
	{regexp.MustCompile(`^FAILED \S+ - (.+)$`), true},                          // pytest summary
	{regexp.MustCompile(`^\s*\d+:\d+\s+error\s+(.+)$`), true},                  // eslint stylish
	{regexp.MustCompile(`^error(\[E\d+\])?: .+`), false},                       // rustc, cargo, generic
	{regexp.MustCompile(`^(Error|ERROR|FATAL|fatal): .+`), false},              // generic tools, git
	{regexp.MustCompile(`^npm (ERR!|error) \S.*`), false},                      // npm
	{regexp.MustCompile(`^(?:[\w.]+\.)?[A-Z]\w*(Error|Exception): .+`), false}, // Python, JS, Java
	{regexp.MustCompile(`^\s*● .+`), false},                                    // jest failing test
	{regexp.MustCompile(`: (command )?not found$`), false},                     // shell
}

// Find returns the first error line in a job log, or "" when none is found.
func Find(trace string) string {
	lines := scriptLines(trace)

	goTest := firstFailingGoTestLine(lines)
	for i, l := range lines {
		if goTest.index >= 0 && i >= goTest.index {
			return goTest.text
		}
		if strings.HasPrefix(l, "$ ") || runnerFailRe.MatchString(l) {
			continue
		}
		if m := locRe.FindStringSubmatch(l); m != nil && !strings.HasSuffix(m[1], "_test.go") {
			return linterRe.ReplaceAllString(clean(m[3]), "") + " (" + path.Base(m[1]) + ":" + m[2] + ")"
		}
		for _, r := range rules {
			if m := r.re.FindStringSubmatch(l); m != nil {
				if r.group {
					return clean(m[1])
				}
				return clean(l)
			}
		}
	}
	if goTest.index >= 0 {
		return goTest.text
	}
	return runnerReason(trace)
}

type match struct {
	index int
	text  string
}

// firstFailingGoTestLine returns the first t.Error/t.Fatal line that belongs
// to a test that failed. Lines from passing tests in -v output are ignored.
func firstFailingGoTestLine(lines []string) match {
	type cand struct {
		index int
		test  string
		text  string
	}
	var cands []cand
	failed := map[string]bool{}
	current := ""
	for i, l := range lines {
		if m := goRunRe.FindStringSubmatch(l); m != nil {
			current = m[1]
			continue
		}
		if m := goFailRe.FindStringSubmatch(l); m != nil {
			failed[m[1]] = true
			// Without -v, details follow the --- FAIL header.
			current = m[1]
			continue
		}
		if m := goTestLineRe.FindStringSubmatch(l); m != nil && current != "" {
			cands = append(cands, cand{i, current, clean(m[3]) + " (" + m[1] + ":" + m[2] + ")"})
		}
	}
	for _, c := range cands {
		if failed[c.test] {
			return match{c.index, c.text}
		}
	}
	return match{index: -1}
}

// scriptLines returns the cleaned lines of the user script sections of the
// log (step_script, build_script, script). Logs without section markers are
// returned whole.
func scriptLines(trace string) []string {
	var all, script []string
	inScript, sawSections := false, false
	for _, raw := range strings.Split(trace, "\n") {
		l := raw
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			// Section markers end with \r\x1b[0K; keep the text after them.
			// Progress output overwrites itself with \r; keep the last frame.
			before := l[:i]
			if m := sectionRe.FindStringSubmatch(ansiRe.ReplaceAllString(before, "")); m != nil {
				sawSections = true
				name := m[2]
				isScript := name == "step_script" || name == "build_script" || name == "script"
				if m[1] == "start" {
					inScript = isScript
				} else if isScript {
					inScript = false
				}
			}
			l = l[i+1:]
		}
		l = strings.TrimRight(ansiRe.ReplaceAllString(l, ""), " \t")
		if l == "" {
			continue
		}
		all = append(all, l)
		if inScript {
			script = append(script, l)
		}
	}
	if sawSections {
		return script
	}
	return all
}

// runnerReason returns the runner's failure reason, e.g. "exit code 1".
func runnerReason(trace string) string {
	reason := ""
	for _, raw := range strings.Split(trace, "\n") {
		l := strings.TrimSpace(ansiRe.ReplaceAllString(raw, ""))
		if i := strings.LastIndex(l, "\r"); i >= 0 {
			l = l[i+1:]
		}
		if m := runnerFailRe.FindStringSubmatch(l); m != nil {
			reason = m[1]
		}
	}
	return reason
}

func clean(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
