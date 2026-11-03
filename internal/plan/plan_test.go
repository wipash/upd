package plan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/platform"
)

func load(t *testing.T, user string) *config.Config {
	t.Helper()
	b, err := config.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	u, err := config.Parse("config.toml", []byte(user))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Merge(b, u)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Every built-in must name the program it needs, either through a plain
// first word in its command or an explicit when.has, so a missing tool
// hides the job instead of failing it.
func TestBuiltinsDeclareTheirProgram(t *testing.T) {
	cfg := load(t, "")
	for _, n := range cfg.Names() {
		j := cfg.Jobs[n]
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if len(j.Run.For(goos)) == 0 {
				continue
			}
			if Has(j, goos) == "" && j.When.Has == nil {
				t.Errorf("%s on %s: first word of run is not a program name; set when.has", n, goos)
			}
		}
	}
}

func TestHas(t *testing.T) {
	tests := map[string]string{
		"claude update":         "claude",
		"FOO=1 brew upgrade":    "",
		"cd ~ && mise upgrade":  "",
		"set -e\nvolta list":    "",
		"./local-script":        "",
		"[ -f x ] && y":         "",
		"yt-dlp -U":             "yt-dlp",
		"juliaup self update":   "juliaup",
		"if true; then x; fi":   "",
		"/usr/bin/env thing go": "",
	}
	for run, want := range tests {
		j := &config.Job{Run: config.Script{config.Cmd{"": run}}}
		if got := Has(j, "linux"); got != want {
			t.Errorf("Has(%q) = %q, want %q", run, got, want)
		}
	}
	watcher := &config.Job{Version: config.Cmd{"": "herdr --version"}}
	if got := Has(watcher, "linux"); got != "herdr" {
		t.Errorf("watcher Has = %q", got)
	}
}

// fakeBin creates an executable named name at dir/sub/name and returns dir/sub.
func fakeBin(t *testing.T, dir, sub, name string) string {
	t.Helper()
	d := filepath.Join(dir, sub)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return d
}

func TestVisibility(t *testing.T) {
	dir := t.TempDir()
	own := fakeBin(t, dir, "home/.local/bin", "claude")
	cellar := fakeBin(t, dir, "brew/Cellar/herdr/1.0/bin", "herdr")
	links := filepath.Join(dir, "brew/bin")
	os.MkdirAll(links, 0o755)
	os.Symlink(filepath.Join(cellar, "herdr"), filepath.Join(links, "herdr"))
	t.Setenv("PATH", own+string(os.PathListSeparator)+links)

	cfg := load(t, "")
	f := platform.Facts{GOOS: "linux", Distro: []string{"bazzite", "fedora"}}
	if r := Visible(cfg, cfg.Jobs["claude"], f); r != "" {
		t.Errorf("claude hidden: %s", r)
	}
	if r := Visible(cfg, cfg.Jobs["herdr"], f); !strings.Contains(r, "herdr is managed by Homebrew") {
		t.Errorf("herdr via a brew symlink: %q", r)
	}
	if r := Visible(cfg, cfg.Jobs["codex"], f); r != "codex is not installed" {
		t.Errorf("codex: %q", r)
	}
	if r := Visible(cfg, cfg.Jobs["apt"], f); !strings.Contains(r, "only for debian") {
		t.Errorf("apt on fedora: %q", r)
	}
	if r := Visible(cfg, cfg.Jobs["sdkman"], platform.Facts{GOOS: "windows"}); !strings.Contains(r, "no command for windows") && !strings.Contains(r, "only for") {
		t.Errorf("sdkman on windows: %q", r)
	}
}

func TestSelection(t *testing.T) {
	dir := t.TempDir()
	bin := fakeBin(t, dir, "bin", "brew")
	fakeBin(t, dir, "bin", "claude")
	fakeBin(t, dir, "bin", "herdr")
	t.Setenv("PATH", bin)
	cfg := load(t, `
disable = ["claude"]
[commands.herdr-restart]
after = ["brew"]
version = "herdr --version"
on_change = "herdr server reload-config"
`)
	f := platform.Facts{GOOS: "linux", Distro: []string{"fedora"}}

	p, err := Build(cfg, f, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if s := p.Entries["claude"].State; s != Disabled {
		t.Errorf("claude state = %v", s)
	}
	if s := p.Entries["brew"].State; s != Run {
		t.Errorf("brew state = %v", s)
	}

	p, err = Build(cfg, f, Options{Names: []string{"brew"}})
	if err != nil {
		t.Fatal(err)
	}
	if s := p.Entries["herdr-restart"].State; s != Run {
		t.Errorf("watcher of a named job: state = %v", s)
	}
	if s := p.Entries["herdr"].State; s != NotNamed {
		t.Errorf("herdr state = %v", s)
	}

	p, err = Build(cfg, f, Options{Names: []string{"claude"}})
	if err != nil || p.Entries["claude"].State != Run {
		t.Errorf("naming a disabled job should run it: %v", err)
	}

	if _, err := Build(cfg, f, Options{Names: []string{"brw"}}); err == nil || !strings.Contains(err.Error(), `did you mean "brew"`) {
		t.Errorf("unknown name: %v", err)
	}
	if _, err := Build(cfg, f, Options{Names: []string{"apt"}}); err == nil || !strings.Contains(err.Error(), "does not apply") {
		t.Errorf("hidden name: %v", err)
	}
}

// A Volta shim on PATH links to a binary that Homebrew installed. The tool
// belongs to Volta, which is what the PATH location says.
func TestManagedByShim(t *testing.T) {
	dir := t.TempDir()
	cellar := fakeBin(t, dir, "brew/Cellar/volta/2.0.2/bin", "volta-shim")
	shims := filepath.Join(dir, "home/.volta/bin")
	os.MkdirAll(shims, 0o755)
	os.Symlink(filepath.Join(cellar, "volta-shim"), filepath.Join(shims, "pnpm"))
	t.Setenv("PATH", shims)

	cfg := load(t, "")
	f := platform.Facts{GOOS: "linux"}
	if r := Visible(cfg, cfg.Jobs["pnpm"], f); !strings.HasPrefix(r, "pnpm is managed by Volta (") {
		t.Errorf("pnpm: %q", r)
	}

	cfg = load(t, `
[managed_paths]
Volta = []
"my installer" = ["*/brew/*"]
`)
	if r := Visible(cfg, cfg.Jobs["pnpm"], f); !strings.HasPrefix(r, "pnpm is managed by Homebrew (") {
		t.Errorf("pnpm with Volta removed: %q", r)
	}
	if got := cfg.ManagedPaths[len(cfg.ManagedPaths)-1].Label; got != "my installer" {
		t.Errorf("last label = %q", got)
	}
}
