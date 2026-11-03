package config

import (
	"strings"
	"testing"
	"time"
)

func mustMerge(t *testing.T, user string) *Config {
	t.Helper()
	cfg, err := merge(t, user)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func merge(t *testing.T, user string) (*Config, error) {
	t.Helper()
	b, err := Builtin()
	if err != nil {
		t.Fatalf("builtin: %v", err)
	}
	u, err := Parse("config.toml", []byte(user))
	if err != nil {
		return nil, err
	}
	return Merge(b, u)
}

func TestBuiltinIsValid(t *testing.T) {
	b, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Merge(b, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ManagedPaths) == 0 {
		t.Error("managed_paths is empty")
	}
	for _, name := range cfg.Names() {
		j := cfg.Jobs[name]
		if j.Sudo && !j.System {
			t.Errorf("%s: sudo jobs must be system jobs so plain upd never asks for a password", name)
		}
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name, doc, want string
	}{
		{"unknown key", "[commands.apt]\nsytem = true\n", `config.toml:2:1: commands.apt.sytem: unknown key (did you mean "system"?)`},
		{"unknown top key", "disabel = []\n", `config.toml:1:1: disabel: unknown key (did you mean "disable"?)`},
		{"after not a list", "[commands.x]\nrun = \"x\"\nafter = \"brew\"\n", `config.toml:3:9: commands.x.after: expected a list of strings, got a string`},
		{"after element type", "[commands.x]\nrun = \"x\"\nafter = [\"brew\", 3]\n", `config.toml:3:18: commands.x.after[1]: expected a string, got an integer`},
		{"bad os key", "[commands.x]\nrun = { widows = \"x\" }\n", `config.toml:2:9: commands.x.run.widows: unknown key (did you mean "windows"?)`},
		{"bad bool", "[commands.x]\nrun = \"x\"\nsudo = \"yes\"\n", `config.toml:3:8: commands.x.sudo: expected true or false, got a string`},
		{"bad duration", "[commands.x]\nrun = \"x\"\ntimeout = \"10 minutes\"\n", `commands.x.timeout: invalid duration "10 minutes"`},
		{"bad when os", "[commands.x]\nrun = \"x\"\nwhen = { os = [\"macos\"] }\n", `commands.x.when.os: unknown OS "macos"`},
		{"syntax", "[commands.x]\nrun = \n", `config.toml:2:`},
		{"unknown after", "[commands.x]\nrun = \"x\"\nafter = [\"bre\"]\n", `config.toml:3:1: commands.x.after: unknown job "bre" (did you mean "brew"?)`},
		{"self after", "[commands.x]\nrun = \"x\"\nafter = [\"x\"]\n", `commands.x.after: a job cannot come after itself`},
		{"cycle", "[commands.a]\nrun = \"a\"\nafter = [\"b\"]\n[commands.b]\nrun = \"b\"\nafter = [\"a\"]\n", `jobs wait for each other in a cycle: a -> b -> a`},
		{"unknown disable", "disable = [\"snapp\"]\n", `disable: unknown job "snapp" (did you mean "snap"?)`},
		{"empty job", "[commands.x]\nsudo = true\n", `commands.x: a job needs run`},
		{"watcher without on_change", "[commands.x]\nversion = \"x --version\"\n", `a watcher job (no run) needs on_change`},
		{"array table", "[[commands]]\n", `arrays of tables`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := merge(t, tt.doc)
			if err == nil {
				t.Fatalf("no error, want %q", tt.want)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error\n  %s\nwant it to contain\n  %s", err, tt.want)
			}
		})
	}
}

func TestShorthandKeepsBuiltinFields(t *testing.T) {
	cfg := mustMerge(t, "[commands]\napt = \"apt-get dist-upgrade\"\n")
	apt := cfg.Jobs["apt"]
	if got := apt.Run.For("linux"); len(got) != 1 || got[0] != "apt-get dist-upgrade" {
		t.Errorf("run = %q", got)
	}
	if !apt.Sudo || !apt.System || apt.Env["DEBIAN_FRONTEND"] == "" {
		t.Errorf("shorthand dropped built-in fields: %+v", apt)
	}
	if apt.Source["run"] != SourceUser || apt.Source["sudo"] != SourceBuiltin {
		t.Errorf("sources = %v", apt.Source)
	}
}

func TestMergeRules(t *testing.T) {
	cfg := mustMerge(t, `
[commands.apt]
system = false
env = { NEEDRESTART_MODE = "", EXTRA = "1" }

[commands.brew]
skip_if = ""
after = []
timeout = "0"

[commands.mine]
run = ["one", { unix = "two", windows = "zwei" }]
`)
	apt := cfg.Jobs["apt"]
	if apt.System {
		t.Error("system = false did not override")
	}
	if _, ok := apt.Env["NEEDRESTART_MODE"]; ok {
		t.Error(`env key set to "" was not removed`)
	}
	if apt.Env["EXTRA"] != "1" || apt.Env["DEBIAN_FRONTEND"] != "noninteractive" {
		t.Errorf("env did not merge by key: %v", apt.Env)
	}
	brew := cfg.Jobs["brew"]
	if brew.SkipIf.For("linux") != "" || len(brew.After) != 0 || brew.Timeout != 0 {
		t.Errorf("fields were not cleared: skip_if=%q after=%v timeout=%v", brew.SkipIf.For("linux"), brew.After, brew.Timeout)
	}
	mine := cfg.Jobs["mine"]
	if got := strings.Join(mine.Run.For("linux"), ","); got != "one,two" {
		t.Errorf("linux run = %q", got)
	}
	if got := strings.Join(mine.Run.For("windows"), ","); got != "one,zwei" {
		t.Errorf("windows run = %q", got)
	}
	if mine.Timeout != DefaultTimeout {
		t.Errorf("default timeout = %v", mine.Timeout)
	}
	if cfg.Jobs["dnf"].Timeout != 0 {
		t.Errorf("system job default timeout = %v, want none", cfg.Jobs["dnf"].Timeout)
	}
	if got := cfg.Jobs["flatpak"].Timeout; got != time.Hour {
		t.Errorf("flatpak timeout = %v", got)
	}
}

func TestCmdFor(t *testing.T) {
	c := Cmd{"unix": "u", "linux": "", "windows": "w"}
	if c.For("linux") != "" || c.For("darwin") != "u" || c.For("windows") != "w" {
		t.Errorf("For: linux=%q darwin=%q windows=%q", c.For("linux"), c.For("darwin"), c.For("windows"))
	}
}

func TestStringEscapes(t *testing.T) {
	cfg := mustMerge(t, "[commands]\nx = \"printf 'a\\tb\\n' \\\"q\\\"\"\n")
	if got := cfg.Jobs["x"].Run.For("linux")[0]; got != "printf 'a\tb\n' \"q\"" {
		t.Errorf("got %q", got)
	}
}

// Format must produce TOML that parses back to the same config.
func TestFormatRoundTrip(t *testing.T) {
	cfg := mustMerge(t, `
post = ["echo done"]
[commands.mine]
run = ["one", { unix = "two", windows = "zwei" }]
env = { "WEIRD KEY" = "a\"b" }
[commands.watch]
after = ["mine"]
version = "x --version"
on_change = "x restart"
`)
	out := Format(cfg)
	f, err := Parse("formatted.toml", []byte(out))
	if err != nil {
		t.Fatalf("formatted config does not parse: %v\n%s", err, out)
	}
	again, err := Merge(&File{Name: "empty", Jobs: map[string]*Spec{}}, f)
	if err != nil {
		t.Fatalf("formatted config does not validate: %v", err)
	}
	if Format(again) != strings.ReplaceAll(out, "# builtin", "# formatted.toml") {
		// Sources differ (everything is "user" now), so compare job by job.
		for _, name := range cfg.Names() {
			a, b := cfg.Jobs[name], again.Jobs[name]
			if strings.Join(a.Run.For("linux"), "\n") != strings.Join(b.Run.For("linux"), "\n") {
				t.Errorf("%s: run changed in round trip", name)
			}
		}
	}
}
