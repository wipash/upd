// Package plan decides which jobs apply to this machine and which of them
// run.
package plan

import (
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/platform"
)

// State is what happens to a job in this run.
type State int

const (
	Run        State = iota // selected to run
	Hidden                  // does not apply to this machine
	Disabled                // listed in disable
	SystemOnly              // a system job without -a
	NotNamed                // jobs were named on the command line and this was not one
)

func (s State) String() string {
	return [...]string{"will run", "hidden", "disabled", "system only (use -a)", "not named"}[s]
}

// Entry is one job and its fate.
type Entry struct {
	Job    *config.Job
	State  State
	Reason string // why the job is hidden
	Watch  bool   // a watcher job (no run command on this OS)
}

// Plan is the set of jobs for one run.
type Plan struct {
	Entries map[string]*Entry
	Names   []string // every job, sorted
}

// Selected returns the jobs that run, sorted by name.
func (p *Plan) Selected() []*Entry {
	var out []*Entry
	for _, n := range p.Names {
		if e := p.Entries[n]; e.State == Run {
			out = append(out, e)
		}
	}
	return out
}

// Options are the command-line choices that affect selection.
type Options struct {
	All   bool
	Names []string
}

// UsageError is a problem with the command line rather than the config.
type UsageError struct{ Msg string }

func (e *UsageError) Error() string { return e.Msg }

// Build evaluates every job against the machine and the options.
func Build(cfg *config.Config, f platform.Facts, opts Options) (*Plan, error) {
	p := &Plan{Entries: map[string]*Entry{}, Names: cfg.Names()}
	for _, n := range p.Names {
		j := cfg.Jobs[n]
		e := &Entry{Job: j, Watch: len(j.Run.For(f.GOOS)) == 0}
		if reason := Visible(cfg, j, f); reason != "" {
			e.State, e.Reason = Hidden, reason
		}
		p.Entries[n] = e
	}

	if len(opts.Names) == 0 {
		for _, n := range p.Names {
			e := p.Entries[n]
			switch {
			case e.State == Hidden:
			case slices.Contains(cfg.Disable, n):
				e.State = Disabled
			case e.Job.System && !opts.All:
				e.State = SystemOnly
			}
		}
		return p, nil
	}

	named := map[string]bool{}
	for _, n := range opts.Names {
		e := p.Entries[n]
		if e == nil {
			msg := fmt.Sprintf("unknown job %q", n)
			if s := config.Closest(n, p.Names); s != "" {
				msg += fmt.Sprintf(" (did you mean %q?)", s)
			}
			return nil, &UsageError{msg}
		}
		if e.State == Hidden {
			return nil, &UsageError{fmt.Sprintf("job %q does not apply to this machine: %s", n, e.Reason)}
		}
		named[n] = true
	}
	// Watchers follow the jobs they watch, so naming brew also runs a
	// watcher that comes after brew.
	for changed := true; changed; {
		changed = false
		for _, n := range p.Names {
			e := p.Entries[n]
			if named[n] || !e.Watch || e.State == Hidden || slices.Contains(cfg.Disable, n) {
				continue
			}
			for _, a := range e.Job.After {
				if named[a] {
					named[n], changed = true, true
					break
				}
			}
		}
	}
	for _, n := range p.Names {
		if e := p.Entries[n]; e.State != Hidden && !named[n] {
			e.State = NotNamed
		}
	}
	return p, nil
}

// Visible returns "" when the job applies to this machine, or the reason
// it does not.
func Visible(cfg *config.Config, j *config.Job, f platform.Facts) string {
	w := j.When
	if len(j.Run.For(f.GOOS)) == 0 && j.Version.For(f.GOOS) == "" {
		return "no command for " + f.GOOS
	}
	if len(w.OS) > 0 && !slices.Contains(w.OS, f.GOOS) {
		return "only for " + strings.Join(w.OS, ", ")
	}
	if len(w.Distro) > 0 {
		if f.GOOS != "linux" {
			return "only for Linux (" + strings.Join(w.Distro, ", ") + ")"
		}
		if !slices.ContainsFunc(w.Distro, func(d string) bool { return slices.Contains(f.Distro, d) }) {
			return fmt.Sprintf("only for %s (this is %s)", strings.Join(w.Distro, ", "), strings.Join(f.Distro, "/"))
		}
	}
	if w.WSL != nil && *w.WSL != f.WSL {
		return ternary(*w.WSL, "only inside WSL", "not inside WSL")
	}
	if w.SSH != nil && *w.SSH != f.SSH {
		return ternary(*w.SSH, "only over SSH", "not over SSH")
	}
	if w.Exists != "" && !f.Exists(w.Exists) {
		return w.Exists + " does not exist"
	}
	if w.Missing != "" && f.Exists(w.Missing) {
		return w.Missing + " exists"
	}
	bin := Has(j, f.GOOS)
	if bin == "" {
		return ""
	}
	_, real, err := platform.Which(bin)
	if err != nil {
		return bin + " is not installed"
	}
	windows := f.GOOS == "windows"
	matchAny := func(globs []string) string {
		for _, g := range globs {
			if platform.MatchPath(g, real, windows) {
				return g
			}
		}
		return ""
	}
	if w.Managed != nil {
		g := matchAny(cfg.ManagedPaths)
		if *w.Managed && g == "" {
			return bin + " was not installed by a package manager (" + real + ")"
		}
		if !*w.Managed && g != "" {
			return bin + " is managed by a package manager (" + real + ")"
		}
	}
	if len(w.Path) > 0 && matchAny(w.Path) == "" {
		return bin + " is at " + real + ", which is not under " + strings.Join(w.Path, ", ")
	}
	if g := matchAny(w.PathNot); g != "" {
		return bin + " is at " + real + " (matches " + g + ")"
	}
	return ""
}

var plainName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`)

// shellWords are words that can start a command line without being a
// program on PATH.
var shellWords = []string{
	"if", "then", "else", "elif", "fi", "for", "while", "until", "do", "done", "case", "esac",
	"function", "select", "time", "set", "cd", "export", "exec", "eval", "source", "test",
	"echo", "printf", "true", "false", "exit", "return", "local", "readonly", "unset", "trap",
	"command", "builtin", "type", "read", "shift", "wait", "umask", "ulimit",
}

// Has returns the program a job needs on PATH, or "" when it does not
// check for one. Without an explicit when.has, it is the first word of the
// job's first run command (or of version, for a watcher), when that word
// is a plain program name.
func Has(j *config.Job, goos string) string {
	if j.When.Has != nil {
		return *j.When.Has
	}
	cmd := j.Version.For(goos)
	if run := j.Run.For(goos); len(run) > 0 {
		cmd = run[0]
	}
	fields := strings.Fields(cmd)
	if len(fields) == 0 {
		return ""
	}
	w := fields[0]
	if !plainName.MatchString(w) || slices.Contains(shellWords, w) {
		return ""
	}
	return w
}

func ternary(c bool, a, b string) string {
	if c {
		return a
	}
	return b
}
