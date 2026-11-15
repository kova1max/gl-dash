package errline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFind(t *testing.T) {
	cases := map[string]string{
		"go-test.log":           "expected 200, got 500 (orders_test.go:88)",
		"go-test-verbose.log":   "expected 200, got 500 (orders_test.go:88)",
		"golangci-lint.log":     "declared and not used: ctx (handler.go:42)",
		"pytest.log":            "assert 25 == 30",
		"eslint.log":            "'ctx' is assigned a value but never used no-unused-vars",
		"jest.log":              "● Orders › creates an order",
		"python-traceback.log":  "django.db.utils.OperationalError: could not connect to server: Connection refused",
		"rust.log":              "error[E0425]: cannot find value `total` in this scope",
		"command-not-found.log": "/bin/sh: eval: line 140: make: not found",
		"no-match.log":          "exit code 3",
		"progress-cr.log":       "Error: checksum mismatch for artifact.tar.gz",
		"no-sections.log":       "unreachable code (orders.go:31)",
		"timeout.log":           "execution took longer than 1h0m0s seconds",
	}
	for file, want := range cases {
		t.Run(file, func(t *testing.T) {
			b, err := os.ReadFile(filepath.Join("testdata", file))
			if err != nil {
				t.Fatal(err)
			}
			if got := Find(string(b)); got != want {
				t.Errorf("Find() = %q, want %q", got, want)
			}
		})
	}
}

func TestFindIgnoresRunnerOutsideScript(t *testing.T) {
	// "ERROR:" lines from the runner before the script must not win.
	trace := "section_start:1:prepare_executor\r\x1b[0K\n" +
		"ERROR: Preparation failed: pull access denied, retrying\n" +
		"section_end:1:prepare_executor\r\x1b[0K\n" +
		"section_start:1:step_script\r\x1b[0K\n" +
		"$ go build ./...\n" +
		"./main.go:9:2: undefined: run\n" +
		"section_end:1:step_script\r\x1b[0K\n" +
		"ERROR: Job failed: exit code 1\n"
	if got, want := Find(trace), "undefined: run (main.go:9)"; got != want {
		t.Errorf("Find() = %q, want %q", got, want)
	}
}

func TestFindEmpty(t *testing.T) {
	if got := Find(""); got != "" {
		t.Errorf("Find(\"\") = %q, want empty", got)
	}
}
