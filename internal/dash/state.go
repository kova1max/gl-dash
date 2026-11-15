package dash

import (
	"context"
	"fmt"

	"github.com/kova1max/gl-dash/internal/config"
)

// State is everything the dashboard knows. It is filled in by applying
// Events; each Event may start more Jobs (connect, then list sections, then
// fetch row details, then explain failed pipelines). The TUI runs Jobs as
// Bubble Tea commands; RunAll runs them to completion for --print.
type State struct {
	Hosts      []HostState
	Sections   []SectionState
	Details    map[Key]Detail
	DetailErr  map[Key]error
	Blockers   map[Key]Blocker
	BlockerErr map[Key]error

	requested map[Key]bool
	pending   int
}

// HostState is the connection state of one host.
type HostState struct {
	Host   config.Host
	Source *Source
	Err    error
	Done   bool
}

// SectionState holds one section's rows merged across hosts.
type SectionState struct {
	Title   string
	Limit   int
	Filter  Filter
	Rows    []Row
	Loading int // hosts still being listed
	Errs    []error
}

// Job does one piece of network work and reports it as an Event.
type Job func(ctx context.Context) Event

// Event is the result of a Job.
type Event interface{ apply(*State) []Job }

// NewState prepares a dashboard for hosts and sections.
func NewState(hosts []config.Host, sections []config.Section) (*State, error) {
	st := &State{
		Details:    map[Key]Detail{},
		DetailErr:  map[Key]error{},
		Blockers:   map[Key]Blocker{},
		BlockerErr: map[Key]error{},
		requested:  map[Key]bool{},
	}
	for _, h := range hosts {
		st.Hosts = append(st.Hosts, HostState{Host: h})
	}
	for _, s := range sections {
		f, err := ParseFilter(s.Filters)
		if err != nil {
			return nil, fmt.Errorf("section %q: %w", s.Title, err)
		}
		st.Sections = append(st.Sections, SectionState{Title: s.Title, Limit: s.Limit, Filter: f, Loading: len(hosts)})
	}
	return st, nil
}

// Start returns the first jobs: one connection per host.
func (st *State) Start() []Job {
	var jobs []Job
	for i := range st.Hosts {
		h := st.Hosts[i].Host
		jobs = append(jobs, func(ctx context.Context) Event {
			src, err := Connect(ctx, h)
			return connected{i, src, err}
		})
	}
	return st.track(jobs)
}

// Apply records an event and returns the jobs it unlocks.
func (st *State) Apply(ev Event) []Job {
	st.pending--
	return st.track(ev.apply(st))
}

// Busy reports how many jobs are still running.
func (st *State) Busy() int { return st.pending }

func (st *State) track(jobs []Job) []Job {
	st.pending += len(jobs)
	return jobs
}

type connected struct {
	host int
	src  *Source
	err  error
}

func (e connected) apply(st *State) []Job {
	hs := &st.Hosts[e.host]
	hs.Source, hs.Err, hs.Done = e.src, e.err, true
	if e.err != nil {
		for i := range st.Sections {
			st.Sections[i].Loading--
		}
		return nil
	}
	var jobs []Job
	for i := range st.Sections {
		sec := st.Sections[i]
		jobs = append(jobs, func(ctx context.Context) Event {
			rows, err := e.src.Section(ctx, sec.Filter, sec.Limit)
			return listed{i, e.src, rows, err}
		})
	}
	return jobs
}

type listed struct {
	section int
	src     *Source
	rows    []Row
	err     error
}

func (e listed) apply(st *State) []Job {
	sec := &st.Sections[e.section]
	sec.Loading--
	if e.err != nil {
		sec.Errs = append(sec.Errs, e.err)
		return nil
	}
	sec.Rows = append(sec.Rows, e.rows...)
	// Each section keeps its own limit after merging hosts.
	SortRows(sec.Rows)
	if len(sec.Rows) > sec.Limit {
		sec.Rows = sec.Rows[:sec.Limit]
	}
	var jobs []Job
	for _, r := range e.rows {
		if st.requested[r.Key] {
			continue
		}
		st.requested[r.Key] = true
		k := r.Key
		jobs = append(jobs, func(ctx context.Context) Event {
			d, err := e.src.Detail(ctx, k)
			return detailed{e.src, k, d, err}
		})
	}
	return jobs
}

type detailed struct {
	src *Source
	key Key
	d   Detail
	err error
}

func (e detailed) apply(st *State) []Job {
	if e.err != nil {
		st.DetailErr[e.key] = e.err
		return nil
	}
	st.Details[e.key] = e.d
	if e.d.Pipeline != "failed" {
		return nil
	}
	return []Job{func(ctx context.Context) Event {
		b, err := e.src.Blocker(ctx, e.key.ProjectID, e.d.PipelineID)
		return explained{e.key, b, err}
	}}
}

type explained struct {
	key Key
	b   Blocker
	err error
}

func (e explained) apply(st *State) []Job {
	st.Blockers[e.key] = e.b
	if e.err != nil {
		st.BlockerErr[e.key] = e.err
	}
	return nil
}

// RunAll runs every job until the state is complete.
func RunAll(ctx context.Context, st *State) {
	events := make(chan Event)
	run := func(jobs []Job) {
		for _, j := range jobs {
			go func() { events <- j(ctx) }()
		}
	}
	run(st.Start())
	for st.Busy() > 0 {
		run(st.Apply(<-events))
	}
}
