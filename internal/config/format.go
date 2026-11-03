package config

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Format renders the merged config as TOML. Every job field carries a
// comment naming the file it came from.
func Format(c *Config) string {
	var b strings.Builder
	userLabel := "user"
	if c.UserFile != "" {
		userLabel = c.UserFile
	}
	fmt.Fprintf(&b, "managed_paths = %s\n", strList(c.ManagedPaths))
	fmt.Fprintf(&b, "disable = %s\n", strList(c.Disable))
	fmt.Fprintf(&b, "max_parallel = %d\n", c.MaxParallel)
	fmt.Fprintf(&b, "min_release_age = \"%dd\"\n", c.MinRelease)
	fmt.Fprintf(&b, "post = %s\n", script(c.Post))
	for _, name := range c.Names() {
		j := c.Jobs[name]
		fmt.Fprintf(&b, "\n[commands.%s]\n", key(name))
		line := func(k, v string) {
			src := j.Source[k]
			if src == "" {
				return
			}
			if src == SourceUser {
				src = userLabel
			}
			fmt.Fprintf(&b, "%s = %s  # %s\n", k, v, src)
		}
		line("run", script(j.Run))
		line("shell", str(j.Shell))
		line("cwd", str(j.Cwd))
		line("env", env(j.Env))
		line("after", strList(j.After))
		line("system", strconv.FormatBool(j.System))
		line("sudo", strconv.FormatBool(j.Sudo))
		line("when", when(j.When))
		line("skip_if", cmd(j.SkipIf))
		line("version", cmd(j.Version))
		line("on_change", cmd(j.OnChange))
		line("timeout", str(duration(j.Timeout)))
		line("ok_exit_codes", intList(j.OkExitCodes))
		line("release_age", env(j.ReleaseAge))
	}
	return b.String()
}

func duration(d time.Duration) string {
	if d == 0 {
		return "0"
	}
	return d.String()
}

func key(k string) string {
	for _, r := range k {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return str(k)
		}
	}
	return k
}

// str renders a TOML string, using a multi-line literal for scripts.
func str(s string) string {
	if strings.Contains(s, "\n") && !strings.Contains(s, "'''") {
		return "'''\n" + s + "'''"
	}
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func strList(l []string) string {
	parts := make([]string, len(l))
	for i, s := range l {
		parts[i] = str(s)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func intList(l []int) string {
	parts := make([]string, len(l))
	for i, n := range l {
		parts[i] = strconv.Itoa(n)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func cmd(c Cmd) string {
	if len(c) == 0 {
		return `""`
	}
	if len(c) == 1 {
		if s, ok := c[""]; ok {
			return str(s)
		}
	}
	var parts []string
	for _, k := range append([]string{""}, OSKeys...) {
		if s, ok := c[k]; ok && k != "" {
			parts = append(parts, k+" = "+str(s))
		}
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func script(s Script) string {
	if len(s) == 1 {
		return cmd(s[0])
	}
	parts := make([]string, len(s))
	for i, c := range s {
		parts[i] = cmd(c)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func env(m map[string]string) string {
	var parts []string
	for _, k := range slices.Sorted(maps.Keys(m)) {
		parts = append(parts, key(k)+" = "+str(m[k]))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func when(w When) string {
	var parts []string
	add := func(k, v string) { parts = append(parts, k+" = "+v) }
	if w.Has != nil {
		add("has", str(*w.Has))
	}
	if w.Managed != nil {
		add("managed", strconv.FormatBool(*w.Managed))
	}
	if w.Path != nil {
		add("path", strList(w.Path))
	}
	if w.PathNot != nil {
		add("path_not", strList(w.PathNot))
	}
	if w.OS != nil {
		add("os", strList(w.OS))
	}
	if w.Distro != nil {
		add("distro", strList(w.Distro))
	}
	if w.WSL != nil {
		add("wsl", strconv.FormatBool(*w.WSL))
	}
	if w.SSH != nil {
		add("ssh", strconv.FormatBool(*w.SSH))
	}
	if w.Exists != "" {
		add("exists", str(w.Exists))
	}
	if w.Missing != "" {
		add("missing", str(w.Missing))
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}
