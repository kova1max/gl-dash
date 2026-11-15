// Command fakegitlab serves seeded fake GitLab instances for the demo
// recording and the benchmark. It never talks to a real GitLab.
//
//	go run ./demo/fakegitlab 127.0.0.1:18081=demo/data/gitlab.com.json 127.0.0.1:18082=demo/data/gitlab.example.dev.json
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/kova1max/gl-dash/internal/fakegitlab"
)

func main() {
	latency := flag.Duration("latency", 0, "delay added to every request")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: fakegitlab [-latency 100ms] addr=dataset.json ...")
		os.Exit(2)
	}
	now := time.Now()
	errc := make(chan error)
	for _, arg := range flag.Args() {
		addr, file, ok := strings.Cut(arg, "=")
		if !ok {
			log.Fatalf("bad argument %q: want addr=dataset.json", arg)
		}
		ds, err := fakegitlab.Load(file)
		if err != nil {
			log.Fatal(err)
		}
		srv := fakegitlab.New(ds, now, *latency)
		log.Printf("serving %s (%d merge requests) on http://%s", ds.Host, len(ds.MRs), addr)
		go func() { errc <- http.ListenAndServe(addr, srv) }()
	}
	log.Fatal(<-errc)
}
