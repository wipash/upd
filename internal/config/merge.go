package config

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"
)

// DefaultTimeout applies to non-system jobs that do not set a timeout.
const DefaultTimeout = 15 * time.Minute

// Sources of a merged field.
const (
	SourceBuiltin = "builtin"
	SourceUser    = "user"
)

// Job is a fully merged job.
type Job struct {
	Name        string
	Run         Script
	Shell       string
	Cwd         string
	Env         map[string]string
	After       []string
	System      bool
	Sudo        bool
	When        When
	SkipIf      Cmd
	Version     Cmd
	OnChange    Cmd
	Timeout     time.Duration // 0 means none
	OkExitCodes []int
	ReleaseAge  map[string]string // env var -> template with {days} or {minutes}

	// Source records where each written field came from, keyed by TOML key.
	Source map[string]string
}

// Config is the merged result of the built-in catalog and the user file.
type Config struct {
	ManagedPaths []ManagedPath
	Disable      []string
	MaxParallel  int
	MinRelease   int // days; 0 means no minimum
	Post         Script
	Jobs         map[string]*Job
	UserFile     string // empty when there is no user file
}

// Names returns all job names, sorted.
func (c *Config) Names() []string {
	return slices.Sorted(maps.Keys(c.Jobs))
}

// Merge layers the user file over the built-in file and validates the result.
// user may be nil.
func Merge(builtin, user *File) (*Config, error) {
	cfg := &Config{Jobs: map[string]*Job{}}
	files := []*File{builtin}
	if user != nil {
		files = append(files, user)
		cfg.UserFile = user.Name
	}
	for _, f := range files {
		src := SourceBuiltin
		if f != builtin {
			src = SourceUser
		}
		for _, m := range f.ManagedPaths {
			cfg.ManagedPaths = setManaged(cfg.ManagedPaths, m)
		}
		if f.Disable != nil {
			cfg.Disable = *f.Disable
		}
		if f.MaxParallel != nil {
			cfg.MaxParallel = *f.MaxParallel
		}
		if f.MinRelease != nil {
			cfg.MinRelease = *f.MinRelease
		}
		if f.Post != nil {
			cfg.Post = *f.Post
		}
		for _, name := range f.Order {
			j := cfg.Jobs[name]
			if j == nil {
				j = &Job{Name: name, Source: map[string]string{}}
				cfg.Jobs[name] = j
			}
			apply(j, f.Jobs[name], src)
		}
	}
	for _, j := range cfg.Jobs {
		if _, set := j.Source["timeout"]; !set && !j.System {
			j.Timeout = DefaultTimeout
		}
		if len(j.OkExitCodes) == 0 {
			j.OkExitCodes = []int{0}
		}
	}
	if err := validate(cfg, files); err != nil {
		return nil, err
	}
	return cfg, nil
}

func apply(j *Job, s *Spec, src string) {
	set := func(key string) { j.Source[key] = src }
	if s.Run != nil {
		j.Run = *s.Run
		set("run")
	}
	if s.Shell != nil {
		j.Shell = *s.Shell
		set("shell")
	}
	if s.Cwd != nil {
		j.Cwd = *s.Cwd
		set("cwd")
	}
	if s.Env != nil {
		j.Env = mergeEnv(j.Env, s.Env)
		set("env")
	}
	if s.After != nil {
		j.After = *s.After
		set("after")
	}
	if s.System != nil {
		j.System = *s.System
		set("system")
	}
	if s.Sudo != nil {
		j.Sudo = *s.Sudo
		set("sudo")
	}
	if s.When != nil {
		j.When = *s.When
		set("when")
	}
	if s.SkipIf != nil {
		j.SkipIf = *s.SkipIf
		set("skip_if")
	}
	if s.Version != nil {
		j.Version = *s.Version
		set("version")
	}
	if s.OnChange != nil {
		j.OnChange = *s.OnChange
		set("on_change")
	}
	if s.Timeout != nil {
		j.Timeout = *s.Timeout
		set("timeout")
	}
	if s.OkExitCodes != nil {
		j.OkExitCodes = *s.OkExitCodes
		set("ok_exit_codes")
	}
	if s.ReleaseAge != nil {
		j.ReleaseAge = mergeEnv(j.ReleaseAge, s.ReleaseAge)
		set("release_age")
	}
}

// setManaged replaces the paths for m.Label, adding the label if it is new.
// An empty list removes the label.
func setManaged(list []ManagedPath, m ManagedPath) []ManagedPath {
	i := slices.IndexFunc(list, func(x ManagedPath) bool { return x.Label == m.Label })
	switch {
	case i < 0 && len(m.Globs) > 0:
		return append(list, m)
	case i >= 0 && len(m.Globs) > 0:
		list[i] = m
	case i >= 0:
		list = slices.Delete(list, i, i+1)
	}
	return list
}

// ManagedBy returns the label of the first managed path that matches.
func (c *Config) ManagedBy(match func(glob string) bool) string {
	for _, m := range c.ManagedPaths {
		if slices.ContainsFunc(m.Globs, match) {
			return m.Label
		}
	}
	return ""
}

// mergeEnv layers over onto base key by key. An empty value removes the key.
func mergeEnv(base, over map[string]string) map[string]string {
	if base == nil {
		base = map[string]string{}
	}
	for k, v := range over {
		if v == "" {
			delete(base, k)
		} else {
			base[k] = v
		}
	}
	return base
}

// ReleaseAgeEnv returns the variables that tell j's tools to skip releases
// younger than c.MinRelease days.
func (c *Config) ReleaseAgeEnv(j *Job) map[string]string {
	if c.MinRelease <= 0 || len(j.ReleaseAge) == 0 {
		return nil
	}
	out := map[string]string{}
	for k, tmpl := range j.ReleaseAge {
		out[k] = fillAge(tmpl, c.MinRelease)
	}
	return out
}

// validate checks references across the whole merged config. It runs on
// every job regardless of OS, so a config is either valid everywhere or
// invalid everywhere.
func validate(cfg *Config, files []*File) error {
	// Report a problem at the position of the file that last wrote the field.
	where := func(job, key string) (string, Position) {
		for i := len(files) - 1; i >= 0; i-- {
			if s := files[i].Jobs[job]; s != nil {
				if p, ok := s.FieldPos[key]; ok {
					return files[i].Name, p
				}
			}
		}
		for i := len(files) - 1; i >= 0; i-- {
			if s := files[i].Jobs[job]; s != nil {
				return files[i].Name, s.Pos
			}
		}
		return files[0].Name, Position{}
	}
	for _, name := range cfg.Names() {
		j := cfg.Jobs[name]
		for _, a := range j.After {
			if a == name {
				f, p := where(name, "after")
				return &Error{File: f, Pos: p, Key: "commands." + name + ".after", Msg: "a job cannot come after itself"}
			}
			if cfg.Jobs[a] == nil {
				f, p := where(name, "after")
				msg := fmt.Sprintf("unknown job %q", a)
				if s := Closest(a, cfg.Names()); s != "" {
					msg += fmt.Sprintf(" (did you mean %q?)", s)
				}
				return &Error{File: f, Pos: p, Key: "commands." + name + ".after", Msg: msg}
			}
		}
		if len(j.Run) == 0 && len(j.Version) == 0 {
			f, p := where(name, "")
			return &Error{File: f, Pos: p, Key: "commands." + name, Msg: "a job needs run, or version and on_change for a watcher job"}
		}
		if len(j.Run) == 0 && len(j.Version) > 0 && len(j.OnChange) == 0 {
			f, p := where(name, "version")
			return &Error{File: f, Pos: p, Key: "commands." + name, Msg: "a watcher job (no run) needs on_change"}
		}
	}
	if cfg.UserFile != "" {
		for _, d := range cfg.Disable {
			if cfg.Jobs[d] == nil {
				msg := fmt.Sprintf("unknown job %q", d)
				if s := Closest(d, cfg.Names()); s != "" {
					msg += fmt.Sprintf(" (did you mean %q?)", s)
				}
				return &Error{File: cfg.UserFile, Key: "disable", Msg: msg}
			}
		}
	}
	if cycle := findCycle(cfg); cycle != nil {
		f, p := where(cycle[0], "after")
		return &Error{File: f, Pos: p, Key: "commands." + cycle[0] + ".after", Msg: "jobs wait for each other in a cycle: " + strings.Join(cycle, " -> ")}
	}
	return nil
}

// findCycle returns one cycle in the after graph as a path that starts and
// ends with the same job, or nil.
func findCycle(cfg *Config) []string {
	const (
		unvisited = iota
		visiting
		done
	)
	state := map[string]int{}
	var stack []string
	var visit func(string) []string
	visit = func(n string) []string {
		state[n] = visiting
		stack = append(stack, n)
		for _, a := range cfg.Jobs[n].After {
			switch state[a] {
			case visiting:
				i := slices.Index(stack, a)
				return append(slices.Clone(stack[i:]), a)
			case unvisited:
				if c := visit(a); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = done
		return nil
	}
	for _, n := range cfg.Names() {
		if state[n] == unvisited {
			if c := visit(n); c != nil {
				return c
			}
		}
	}
	return nil
}
