// Command gl-dash is a read-only terminal dashboard of the GitLab merge
// requests waiting on you, across gitlab.com and self-managed instances.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"runtime/debug"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/kova1max/gl-dash/internal/config"
	"github.com/kova1max/gl-dash/internal/dash"
	"github.com/kova1max/gl-dash/internal/ui"
)

// version is set by the release build.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gl-dash:", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "config file (default "+config.DefaultPath()+")")
	printMode := flag.Bool("print", false, "load everything, print it as text and exit")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println("gl-dash", buildVersion())
		return nil
	}

	path, explicit := config.DefaultPath(), *configPath != ""
	if explicit {
		path = *configPath
	}
	cfg, err := config.Load(path, explicit)
	if err != nil {
		return err
	}
	hosts, err := config.ResolveHosts(cfg, config.SystemEnv())
	if err != nil {
		return err
	}

	if *printMode {
		st, err := dash.NewState(hosts, cfg.MRSections)
		if err != nil {
			return err
		}
		dash.RunAll(context.Background(), st)
		width := 120
		if w, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && w > 0 {
			width = w
		}
		ui.Print(os.Stdout, st, time.Now(), width)
		for _, h := range st.Hosts {
			if h.Err != nil {
				return fmt.Errorf("%d of %d hosts failed", countErrs(st), len(st.Hosts))
			}
		}
		return nil
	}

	m, err := ui.New(hosts, cfg.MRSections, time.Now, openURL)
	if err != nil {
		return err
	}
	_, err = tea.NewProgram(m).Run()
	return err
}

func countErrs(st *dash.State) int {
	n := 0
	for _, h := range st.Hosts {
		if h.Err != nil {
			n++
		}
	}
	return n
}

func openURL(url string) error {
	var cmd *exec.Cmd
	switch {
	case os.Getenv("BROWSER") != "":
		cmd = exec.Command(os.Getenv("BROWSER"), url)
	case runtime.GOOS == "darwin":
		cmd = exec.Command("open", url)
	case runtime.GOOS == "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return version
}
