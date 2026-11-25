package plan

import (
	"os"
	"path/filepath"
	"runtime"
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

func TestPolkitAndFlatpakSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX paths and scripts")
	}
	dir := t.TempDir()
	bin := fakeBin(t, dir, "bin", "flatpak")
	fakeBin(t, dir, "bin", "rpm-ostree")
	fakeBin(t, dir, "bin", "brew")
	t.Setenv("PATH", bin)

	home := filepath.Join(dir, "home")
	root := filepath.Join(dir, "root")
	os.MkdirAll(filepath.Join(root, "run"), 0o755)
	os.WriteFile(filepath.Join(root, "run", "ostree-booted"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "var/lib/flatpak/app"), 0o755)
	// repo exists without app/, as on a desktop with no user flatpaks.
	os.MkdirAll(filepath.Join(home, ".local/share/flatpak/repo"), 0o755)
	t.Setenv("UPD_TEST_ROOT", root)

	cfg := load(t, "")
	baseFacts := func(polkit func(string) bool) platform.Facts {
		f := platform.Detect()
		f.GOOS = "linux"
		f.Home = home
		f.Root = false
		f.CheckPolkit = polkit
		return f
	}

	t.Run("plain upd with polkit denied skips flatpak and does not probe rpm-ostree", func(t *testing.T) {
		var probed []string
		f := baseFacts(func(action string) bool {
			probed = append(probed, action)
			return false
		})
		p, err := Build(cfg, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak-user"].State != Hidden {
			t.Errorf("flatpak-user state = %v, want Hidden", p.Entries["flatpak-user"].State)
		}
		if p.Entries["rpm-ostree"].State != SystemOnly {
			t.Errorf("rpm-ostree state = %v, want SystemOnly", p.Entries["rpm-ostree"].State)
		}
		fp := p.Entries["flatpak"]
		if fp.State != Run || fp.Sudo || fp.SkipReason != "needs sudo (use upd -a)" {
			t.Errorf("flatpak = %+v", fp)
		}
		if len(probed) != 1 || probed[0] != "org.freedesktop.Flatpak.app-update" {
			t.Errorf("probed = %v, want only first flatpak action (short-circuit)", probed)
		}
		if !cfg.Jobs["flatpak"].Sudo {
			t.Error("Build mutated config.Job.Sudo")
		}
	})

	t.Run("disabled and not-named jobs do not probe polkit", func(t *testing.T) {
		probed := false
		f := baseFacts(func(string) bool { probed = true; return true })

		disabledCfg := load(t, `disable = ["flatpak"]`)
		p, err := Build(disabledCfg, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak"].State != Disabled || probed {
			t.Errorf("disabled flatpak: state=%v probed=%v", p.Entries["flatpak"].State, probed)
		}

		p, err = Build(cfg, f, Options{Names: []string{"brew"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak"].State != NotNamed || p.Entries["rpm-ostree"].State != NotNamed || probed {
			t.Errorf("not-named: flatpak=%v rpm-ostree=%v probed=%v", p.Entries["flatpak"].State, p.Entries["rpm-ostree"].State, probed)
		}
	})

	t.Run("plain upd and named runs with polkit allowed run without sudo", func(t *testing.T) {
		f := baseFacts(func(string) bool { return true })
		p, err := Build(cfg, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		fp := p.Entries["flatpak"]
		if fp.State != Run || fp.Sudo || fp.SkipReason != "" {
			t.Errorf("plain flatpak = %+v", fp)
		}

		p, err = Build(cfg, f, Options{Names: []string{"flatpak", "rpm-ostree"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak"].Sudo || p.Entries["rpm-ostree"].Sudo {
			t.Errorf("named allowed jobs should not need sudo: flatpak=%v rpm-ostree=%v", p.Entries["flatpak"].Sudo, p.Entries["rpm-ostree"].Sudo)
		}
	})

	t.Run("partial polkit allow still needs sudo", func(t *testing.T) {
		f := baseFacts(func(action string) bool {
			return action != "org.freedesktop.Flatpak.runtime-uninstall"
		})
		p, err := Build(cfg, f, Options{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if !p.Entries["flatpak"].Sudo {
			t.Error("expected flatpak to need sudo when runtime-uninstall is denied")
		}
		if p.Entries["rpm-ostree"].Sudo {
			t.Error("expected rpm-ostree not to need sudo when upgrade is allowed")
		}
	})

	t.Run("all and named jobs use sudo when polkit denies", func(t *testing.T) {
		f := baseFacts(nil)
		p, err := Build(cfg, f, Options{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if !p.Entries["flatpak"].Sudo || p.Entries["flatpak"].SkipReason != "" {
			t.Errorf("upd -a flatpak = %+v", p.Entries["flatpak"])
		}
		if !p.Entries["rpm-ostree"].Sudo {
			t.Errorf("upd -a rpm-ostree = %+v", p.Entries["rpm-ostree"])
		}

		p, err = Build(cfg, f, Options{Names: []string{"flatpak"}})
		if err != nil {
			t.Fatal(err)
		}
		if !p.Entries["flatpak"].Sudo || p.Entries["flatpak"].SkipReason != "" {
			t.Errorf("upd flatpak = %+v", p.Entries["flatpak"])
		}
	})

	t.Run("root skips sudo and polkit on plain and all runs", func(t *testing.T) {
		probed := false
		f := baseFacts(func(string) bool { probed = true; return false })
		f.Root = true

		p, err := Build(cfg, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak"].Sudo || p.Entries["flatpak"].SkipReason != "" || probed {
			t.Errorf("root plain upd flatpak = %+v probed=%v", p.Entries["flatpak"], probed)
		}

		p, err = Build(cfg, f, Options{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak"].Sudo || p.Entries["rpm-ostree"].Sudo || probed {
			t.Errorf("root should not need sudo or probe polkit (probed=%v)", probed)
		}
	})

	t.Run("auto-included watchers respect system on named runs", func(t *testing.T) {
		watchCfg := load(t, `
[commands.watch-always]
after = ["brew"]
system = true
sudo = true
version = "brew --version"
on_change = "true"

[commands.watch-ifsudo]
after = ["brew"]
system = "sudo"
sudo = true
polkit = ["act.watch"]
version = "brew --version"
on_change = "true"
`)
		p, err := Build(watchCfg, baseFacts(nil), Options{Names: []string{"brew"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["watch-always"].State != NotNamed {
			t.Errorf("system=true watcher should not auto-include without -a: state=%v", p.Entries["watch-always"].State)
		}
		wi := p.Entries["watch-ifsudo"]
		if wi.State != Run || wi.Sudo || wi.SkipReason != "needs sudo (use upd -a)" {
			t.Errorf("system=sudo watcher without -a = %+v", wi)
		}

		p, err = Build(watchCfg, baseFacts(nil), Options{All: true, Names: []string{"brew"}})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["watch-always"].State != Run || !p.Entries["watch-always"].Sudo {
			t.Errorf("system=true watcher with -a = %+v", p.Entries["watch-always"])
		}
		if p.Entries["watch-ifsudo"].State != Run || !p.Entries["watch-ifsudo"].Sudo || p.Entries["watch-ifsudo"].SkipReason != "" {
			t.Errorf("system=sudo watcher with -a = %+v", p.Entries["watch-ifsudo"])
		}
	})

	t.Run("user overrides compose cleanly", func(t *testing.T) {
		f := baseFacts(func(string) bool { return true })
		// system = true keeps flatpak behind -a even when polkit allows.
		c1 := load(t, "[commands.flatpak]\nsystem = true\n")
		p1, err := Build(c1, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p1.Entries["flatpak"].State != SystemOnly {
			t.Errorf("system = true: state = %v, want SystemOnly", p1.Entries["flatpak"].State)
		}

		// system = false runs with sudo on plain upd when polkit denies.
		c2 := load(t, "[commands.flatpak]\nsystem = false\n")
		p2, err := Build(c2, baseFacts(nil), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if !p2.Entries["flatpak"].Sudo || p2.Entries["flatpak"].SkipReason != "" {
			t.Errorf("system = false: %+v", p2.Entries["flatpak"])
		}

		// sudo = false never elevates or probes polkit.
		probed := false
		c3 := load(t, "[commands.flatpak]\nsudo = false\n")
		p3, err := Build(c3, baseFacts(func(string) bool { probed = true; return false }), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p3.Entries["flatpak"].Sudo || p3.Entries["flatpak"].SkipReason != "" || probed {
			t.Errorf("sudo = false: %+v probed=%v", p3.Entries["flatpak"], probed)
		}

		// polkit = [] skips probing and skips flatpak on plain upd.
		probed = false
		c4 := load(t, "[commands.flatpak]\npolkit = []\n")
		p4, err := Build(c4, baseFacts(func(string) bool { probed = true; return true }), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p4.Entries["flatpak"].SkipReason != "needs sudo (use upd -a)" || probed {
			t.Errorf("polkit = []: %+v probed=%v", p4.Entries["flatpak"], probed)
		}
	})

	t.Run("flatpak-user appears when app dir exists", func(t *testing.T) {
		userHome := t.TempDir()
		os.MkdirAll(filepath.Join(userHome, ".local/share/flatpak/app"), 0o755)
		f := baseFacts(nil)
		f.Home = userHome
		p, err := Build(cfg, f, Options{})
		if err != nil {
			t.Fatal(err)
		}
		if p.Entries["flatpak-user"].State != Run || p.Entries["flatpak-user"].Sudo {
			t.Errorf("flatpak-user = %+v", p.Entries["flatpak-user"])
		}
	})
}
