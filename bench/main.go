// Command bench measures how long gl-dash takes to show every merge request
// waiting on you across two GitLab hosts, including the first error line of
// each failed pipeline, against seeded fake GitLab servers with a fixed
// simulated network latency. It also counts the page loads the same answer
// takes in a browser. Run it from the repository root:
//
//	go run ./bench > bench/results.txt
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/kova1max/gl-dash/internal/fakegitlab"
)

func main() {
	latency := flag.Duration("latency", 200*time.Millisecond, "simulated latency added to every API request")
	runs := flag.Int("runs", 5, "number of timed runs")
	flag.Parse()

	tmp, err := os.MkdirTemp("", "gl-dash-bench")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	bin := filepath.Join(tmp, "gl-dash")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		log.Fatalf("build: %v\n%s", err, out)
	}

	files := []string{"demo/data/gitlab.com.json", "demo/data/gitlab.example.dev.json"}
	var servers []*fakegitlab.Server
	var cfg strings.Builder
	cfg.WriteString("hosts:\n")
	failed, mrs := 0, 0
	for _, f := range files {
		ds, err := fakegitlab.Load(f)
		if err != nil {
			log.Fatal(err)
		}
		srv := fakegitlab.New(ds, time.Now(), *latency)
		ts := httptest.NewServer(srv)
		defer ts.Close()
		servers = append(servers, srv)
		fmt.Fprintf(&cfg, "  - host: %s\n    api_host: %s\n    api_protocol: http\n    token: %s\n", ds.Host, strings.TrimPrefix(ts.URL, "http://"), ds.Token)
		for _, mr := range ds.MRs {
			mrs++
			if mr.Pipeline != nil && mr.Pipeline.Status == "failed" {
				failed++
			}
		}
	}
	cfgPath := filepath.Join(tmp, "config.yml")
	if err := os.WriteFile(cfgPath, []byte(cfg.String()), 0o600); err != nil {
		log.Fatal(err)
	}

	var times []time.Duration
	var lastOut string
	var requests int64
	for i := 0; i < *runs; i++ {
		before := total(servers)
		start := time.Now()
		cmd := exec.Command(bin, "--print", "--config", cfgPath)
		cmd.Env = append(os.Environ(), "COLUMNS=110")
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)
		if err != nil {
			log.Fatalf("gl-dash --print: %v\n%s", err, out)
		}
		times = append(times, elapsed)
		requests = total(servers) - before
		lastOut = string(out)
	}
	slices.Sort(times)
	errLines := map[string]bool{}
	for _, l := range strings.Split(lastOut, "\n") {
		if strings.HasPrefix(l, "    ✗ ") {
			errLines[l] = true
		}
	}

	fmt.Printf("# gl-dash benchmark (go run ./bench)\n")
	fmt.Printf("date: %s\n", time.Now().UTC().Format("2006-01-02"))
	fmt.Printf("machine: %s/%s, %d CPUs, %s\n", runtime.GOOS, runtime.GOARCH, runtime.NumCPU(), runtime.Version())
	fmt.Printf("dataset: %d hosts, %d seeded merge requests, %d with a failed pipeline (demo/data)\n", len(files), mrs, failed)
	fmt.Printf("simulated latency per API request: %s\n\n", *latency)
	fmt.Printf("gl-dash --print, from process start until every row and error line is printed:\n")
	for i, t := range times {
		fmt.Printf("  run %d: %.2fs\n", i+1, t.Seconds())
	}
	fmt.Printf("  median: %.2fs\n", times[len(times)/2].Seconds())
	fmt.Printf("  API requests per run: %d (all GET)\n", requests)
	fmt.Printf("  serial lower bound for the same requests: %.1fs (%d x %s)\n", (time.Duration(requests) * *latency).Seconds(), requests, *latency)
	fmt.Printf("  distinct failed jobs with an error line shown: %d of %d\n\n", len(errLines), failed)
	fmt.Printf("browser, counted page loads for the same answer:\n")
	fmt.Printf("  %d merge request homepages (one per host)\n", len(files))
	fmt.Printf("  + %d failed pipelines x 3 (merge request, pipeline, failed job log) = %d\n", failed, failed*3)
	fmt.Printf("  total: %d page loads\n\n", len(files)+failed*3)
	fmt.Printf("last gl-dash --print output:\n\n%s", lastOut)
}

func total(servers []*fakegitlab.Server) int64 {
	var n int64
	for _, s := range servers {
		n += s.Requests()
	}
	return n
}
