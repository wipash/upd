package ui

import (
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/wipash/upd/internal/run"
)

// Plain prints one line per event. With Verbose it also prints every line
// of every job's output, prefixed with the job name.
type Plain struct {
	W       io.Writer
	Style   Style
	Verbose bool

	mu      sync.Mutex
	watches map[string]bool
}

// SetWatchers names the watcher jobs, which are only reported when they
// do something.
func (p *Plain) SetWatchers(names []string) {
	p.watches = map[string]bool{}
	for _, n := range names {
		p.watches[n] = true
	}
}

func (p *Plain) printf(format string, a ...any) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fmt.Fprintf(p.W, "%s "+format+"\n", append([]any{time.Now().Format("15:04:05")}, a...)...)
}

func (p *Plain) JobWaiting(name string, on []string) {
	if !p.watches[name] {
		p.printf("wait  %s (after %s)", name, strings.Join(on, ", "))
	}
}

func (p *Plain) JobStarted(name string) {
	if !p.watches[name] {
		p.printf("start %s", name)
	}
}

func (p *Plain) JobOutput(name string, lines []string, _ string) {
	if !p.Verbose {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, l := range lines {
		fmt.Fprintf(p.W, "%s | %s\n", p.Style.Dim(name), l)
	}
}

func (p *Plain) JobFinished(r *run.Result) {
	if p.watches[r.Name] && (r.Status == run.UpToDate || r.Status == run.Done || r.Status == run.NotRun) {
		return
	}
	dur := ""
	if !r.Start.IsZero() {
		dur = " " + formatDuration(r.End.Sub(r.Start))
	}
	label := map[run.Status]string{run.Updated: "updated", run.Done: "ok", run.UpToDate: "ok", run.Skipped: "skip", run.NotRun: "not run"}[r.Status]
	if label == "" {
		label = p.Style.Red("FAIL")
	}
	d := detail(r)
	if r.Status == run.Skipped || r.Status == run.NotRun {
		d = r.Detail
	}
	p.printf("%-5s %s%s  %s", label, r.Name, dur, d)
}
