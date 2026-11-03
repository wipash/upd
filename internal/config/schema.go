package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/pelletier/go-toml/v2/unstable"
)

// Error is a config problem, located in a file when possible.
type Error struct {
	File string
	Pos  Position
	Key  string
	Msg  string
}

func (e *Error) Error() string {
	var b strings.Builder
	b.WriteString(e.File)
	if e.Pos.Line > 0 {
		fmt.Fprintf(&b, ":%d:%d", e.Pos.Line, e.Pos.Col)
	}
	b.WriteString(": ")
	if e.Key != "" {
		b.WriteString(e.Key)
		b.WriteString(": ")
	}
	b.WriteString(e.Msg)
	return b.String()
}

// OSKeys are the keys allowed in a per-OS command table.
var OSKeys = []string{"unix", "linux", "darwin", "windows"}

// Cmd is a shell command that may differ per OS. The key "" applies to
// every OS. An empty command string means "no command".
type Cmd map[string]string

// For returns the command for goos, preferring the most specific key.
func (c Cmd) For(goos string) string {
	if s, ok := c[goos]; ok {
		return s
	}
	if goos != "windows" {
		if s, ok := c["unix"]; ok {
			return s
		}
	}
	return c[""]
}

// Script is a list of commands run in order.
type Script []Cmd

// For returns the non-empty commands for goos.
func (s Script) For(goos string) []string {
	var out []string
	for _, c := range s {
		if cmd := c.For(goos); cmd != "" {
			out = append(out, cmd)
		}
	}
	return out
}

// When holds the static predicates that decide whether a job applies to
// this machine. All set predicates must hold.
type When struct {
	Has     *string // nil: derived from the job's commands; "": no binary check
	Managed *bool   // the has binary resolves into one of Config.ManagedPaths
	Path    []string
	PathNot []string
	OS      []string
	Distro  []string
	WSL     *bool
	SSH     *bool
	Exists  string
	Missing string
}

// Spec is one job as written in a single file. A nil field was not written,
// so a user Spec can be layered over a built-in Spec field by field.
type Spec struct {
	Run         *Script
	Shell       *string
	Cwd         *string
	Env         map[string]string
	After       *[]string
	System      *bool
	Sudo        *bool
	When        *When
	SkipIf      *Cmd
	Version     *Cmd
	OnChange    *Cmd
	Timeout     *time.Duration
	OkExitCodes *[]int

	Pos      Position            // position of the job's key
	FieldPos map[string]Position // position of each written field
}

// File is one decoded config file.
type File struct {
	Name         string
	ManagedPaths *[]string
	Disable      *[]string
	MaxParallel  *int
	Post         *Script
	Jobs         map[string]*Spec
	Order        []string // job names in document order
}

var (
	topKeys  = []string{"managed_paths", "disable", "max_parallel", "post", "commands"}
	jobKeys  = []string{"run", "shell", "cwd", "env", "after", "system", "sudo", "when", "skip_if", "version", "on_change", "timeout", "ok_exit_codes"}
	whenKeys = []string{"has", "managed", "path", "path_not", "os", "distro", "wsl", "ssh", "exists", "missing"}
	osValues = []string{"linux", "darwin", "windows"}
)

// Parse decodes a config file.
func Parse(name string, doc []byte) (*File, error) {
	root, err := parseTree(name, doc)
	if err != nil {
		return nil, err
	}
	d := decoder{name: name}
	return d.decodeFile(root)
}

type decoder struct{ name string }

func (d decoder) errf(p Position, key, format string, a ...any) error {
	return &Error{File: d.name, Pos: p, Key: key, Msg: fmt.Sprintf(format, a...)}
}

func kindName(v *value) string {
	switch v.kind {
	case unstable.Table:
		return "table"
	case unstable.Array:
		return "list"
	case unstable.String:
		return "string"
	case unstable.Bool:
		return "boolean"
	case unstable.Integer:
		return "integer"
	case unstable.Float:
		return "float"
	default:
		return "date/time"
	}
}

func article(s string) string {
	if strings.IndexByte("aeiou", s[0]) >= 0 {
		return "an " + s
	}
	return "a " + s
}

func (d decoder) wrongType(v *value, key, want string) error {
	return d.errf(v.pos, key, "expected %s, got %s", want, article(kindName(v)))
}

func (d decoder) unknown(f *field, key string, allowed []string) error {
	msg := "unknown key"
	if s := Closest(f.name, allowed); s != "" {
		msg += fmt.Sprintf(" (did you mean %q?)", s)
	}
	return d.errf(f.keyPos, key, "%s", msg)
}

func (d decoder) decodeFile(root *value) (*File, error) {
	out := &File{Name: d.name, Jobs: map[string]*Spec{}}
	for _, f := range root.fields {
		var err error
		switch f.name {
		case "managed_paths":
			var l []string
			l, err = d.strList(f.val, f.name)
			out.ManagedPaths = &l
		case "disable":
			var l []string
			l, err = d.strList(f.val, f.name)
			out.Disable = &l
		case "max_parallel":
			var n int
			n, err = d.int(f.val, f.name)
			if err == nil && n < 0 {
				err = d.errf(f.val.pos, f.name, "must be 0 (unlimited) or more")
			}
			out.MaxParallel = &n
		case "post":
			out.Post, err = d.script(f.val, f.name)
		case "commands":
			if f.val.kind != unstable.Table {
				return nil, d.wrongType(f.val, f.name, "a table")
			}
			for _, jf := range f.val.fields {
				key := "commands." + jf.name
				spec, err := d.job(jf.val, key)
				if err != nil {
					return nil, err
				}
				spec.Pos = jf.keyPos
				out.Jobs[jf.name] = spec
				out.Order = append(out.Order, jf.name)
			}
		default:
			err = d.unknown(f, f.name, topKeys)
		}
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (d decoder) job(v *value, key string) (*Spec, error) {
	s := &Spec{FieldPos: map[string]Position{}}
	if v.kind == unstable.String {
		s.Run = &Script{Cmd{"": v.text}}
		s.FieldPos["run"] = v.pos
		return s, nil
	}
	if v.kind != unstable.Table {
		return nil, d.wrongType(v, key, "a command string or a table")
	}
	for _, f := range v.fields {
		k := key + "." + f.name
		s.FieldPos[f.name] = f.keyPos
		var err error
		switch f.name {
		case "run":
			s.Run, err = d.script(f.val, k)
		case "shell":
			s.Shell, err = d.strPtr(f.val, k)
		case "cwd":
			s.Cwd, err = d.strPtr(f.val, k)
		case "env":
			s.Env, err = d.env(f.val, k)
		case "after":
			var l []string
			l, err = d.strList(f.val, k)
			s.After = &l
		case "system":
			s.System, err = d.boolPtr(f.val, k)
		case "sudo":
			s.Sudo, err = d.boolPtr(f.val, k)
		case "when":
			s.When, err = d.when(f.val, k)
		case "skip_if":
			s.SkipIf, err = d.cmd(f.val, k)
		case "version":
			s.Version, err = d.cmd(f.val, k)
		case "on_change":
			s.OnChange, err = d.cmd(f.val, k)
		case "timeout":
			s.Timeout, err = d.duration(f.val, k)
		case "ok_exit_codes":
			var l []int
			l, err = d.intList(f.val, k)
			s.OkExitCodes = &l
		default:
			err = d.unknown(f, k, jobKeys)
		}
		if err != nil {
			return nil, err
		}
	}
	return s, nil
}

func (d decoder) when(v *value, key string) (*When, error) {
	if v.kind != unstable.Table {
		return nil, d.wrongType(v, key, "a table")
	}
	w := &When{}
	for _, f := range v.fields {
		k := key + "." + f.name
		var err error
		switch f.name {
		case "has":
			w.Has, err = d.strPtr(f.val, k)
		case "managed":
			w.Managed, err = d.boolPtr(f.val, k)
		case "path":
			w.Path, err = d.strList(f.val, k)
		case "path_not":
			w.PathNot, err = d.strList(f.val, k)
		case "os":
			w.OS, err = d.strList(f.val, k)
			for i, o := range w.OS {
				if !contains(osValues, o) && err == nil {
					err = d.errf(f.val.items[i].pos, k, "unknown OS %q (expected one of %s)", o, strings.Join(osValues, ", "))
				}
			}
		case "distro":
			w.Distro, err = d.strList(f.val, k)
		case "wsl":
			w.WSL, err = d.boolPtr(f.val, k)
		case "ssh":
			w.SSH, err = d.boolPtr(f.val, k)
		case "exists":
			w.Exists, err = d.str(f.val, k)
		case "missing":
			w.Missing, err = d.str(f.val, k)
		default:
			err = d.unknown(f, k, whenKeys)
		}
		if err != nil {
			return nil, err
		}
	}
	return w, nil
}

// script accepts a command, or a list of commands.
func (d decoder) script(v *value, key string) (*Script, error) {
	if v.kind != unstable.Array {
		c, err := d.cmd(v, key)
		if err != nil {
			return nil, err
		}
		if c.For("linux") == "" && c.For("darwin") == "" && c.For("windows") == "" {
			return &Script{}, nil
		}
		return &Script{*c}, nil
	}
	s := Script{}
	for i, it := range v.items {
		c, err := d.cmd(it, fmt.Sprintf("%s[%d]", key, i))
		if err != nil {
			return nil, err
		}
		s = append(s, *c)
	}
	return &s, nil
}

// cmd accepts a string, or a table keyed by OS.
func (d decoder) cmd(v *value, key string) (*Cmd, error) {
	switch v.kind {
	case unstable.String:
		return &Cmd{"": v.text}, nil
	case unstable.Table:
		c := Cmd{}
		for _, f := range v.fields {
			k := key + "." + f.name
			if !contains(OSKeys, f.name) {
				return nil, d.unknown(f, k, OSKeys)
			}
			s, err := d.str(f.val, k)
			if err != nil {
				return nil, err
			}
			c[f.name] = s
		}
		return &c, nil
	}
	return nil, d.wrongType(v, key, "a command string or a table keyed by OS (unix, linux, darwin, windows)")
}

func (d decoder) env(v *value, key string) (map[string]string, error) {
	if v.kind != unstable.Table {
		return nil, d.wrongType(v, key, "a table")
	}
	m := map[string]string{}
	for _, f := range v.fields {
		s, err := d.str(f.val, key+"."+f.name)
		if err != nil {
			return nil, err
		}
		m[f.name] = s
	}
	return m, nil
}

func (d decoder) str(v *value, key string) (string, error) {
	if v.kind != unstable.String {
		return "", d.wrongType(v, key, "a string")
	}
	return v.text, nil
}

func (d decoder) strPtr(v *value, key string) (*string, error) {
	s, err := d.str(v, key)
	return &s, err
}

func (d decoder) boolPtr(v *value, key string) (*bool, error) {
	if v.kind != unstable.Bool {
		return nil, d.wrongType(v, key, "true or false")
	}
	b := v.text == "true"
	return &b, nil
}

func (d decoder) int(v *value, key string) (int, error) {
	if v.kind != unstable.Integer {
		return 0, d.wrongType(v, key, "an integer")
	}
	n, err := strconv.ParseInt(strings.ReplaceAll(v.text, "_", ""), 0, 64)
	if err != nil {
		return 0, d.errf(v.pos, key, "invalid integer %q", v.text)
	}
	return int(n), nil
}

func (d decoder) duration(v *value, key string) (*time.Duration, error) {
	s, err := d.str(v, key)
	if err != nil {
		return nil, d.wrongType(v, key, `a duration string such as "30m" or "0" for none`)
	}
	if s == "" || s == "0" {
		var zero time.Duration
		return &zero, nil
	}
	t, err := time.ParseDuration(s)
	if err != nil || t < 0 {
		return nil, d.errf(v.pos, key, `invalid duration %q (examples: "90s", "30m", "1h", "0" for none)`, s)
	}
	return &t, nil
}

func (d decoder) strList(v *value, key string) ([]string, error) {
	if v.kind != unstable.Array {
		return nil, d.wrongType(v, key, "a list of strings")
	}
	out := []string{}
	for i, it := range v.items {
		s, err := d.str(it, fmt.Sprintf("%s[%d]", key, i))
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, nil
}

func (d decoder) intList(v *value, key string) ([]int, error) {
	if v.kind != unstable.Array {
		return nil, d.wrongType(v, key, "a list of integers")
	}
	out := []int{}
	for i, it := range v.items {
		n, err := d.int(it, fmt.Sprintf("%s[%d]", key, i))
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, nil
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// Closest returns the allowed word within edit distance 2 of k, if any.
func Closest(k string, allowed []string) string {
	best, bestDist := "", 3
	for _, a := range allowed {
		if dist := levenshtein(k, a); dist < bestDist {
			best, bestDist = a, dist
		}
	}
	return best
}

func levenshtein(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j-1]+cost, prev[j]+1, cur[j-1]+1)
		}
		prev = cur
	}
	return prev[len(b)]
}
