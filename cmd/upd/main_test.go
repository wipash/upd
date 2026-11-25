package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/wipash/upd/internal/config"
	"github.com/wipash/upd/internal/plan"
	"github.com/wipash/upd/internal/platform"
	"github.com/wipash/upd/internal/run"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"upd": func() { os.Exit(mainErr(os.Args[1:])) },
	})
}

// The scripts run with a PATH that holds only basic shell tools plus
// $WORK/bin, so the real package managers on the test machine stay hidden
// and each script installs fakes of the tools it needs.
func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir: "testdata/script",
		Setup: func(env *testscript.Env) error {
			sys := filepath.Join(env.WorkDir, ".sysbin")
			if err := os.MkdirAll(sys, 0o755); err != nil {
				return err
			}
			for _, tool := range []string{"sh", "sleep", "cat", "echo", "printf", "true", "false", "env", "grep", "awk", "seq", "touch", "date", "tr", "head"} {
				p, err := exec.LookPath(tool)
				if err != nil {
					return err
				}
				if err := os.Symlink(p, filepath.Join(sys, tool)); err != nil {
					return err
				}
			}
			bin := filepath.Join(env.WorkDir, "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				return err
			}
			// testscript puts the directory holding the upd command first;
			// the rest of the inherited PATH is dropped.
			cmdDir := filepath.SplitList(env.Getenv("PATH"))[0]
			env.Setenv("PATH", strings.Join([]string{cmdDir, bin, sys}, string(os.PathListSeparator)))
			home := filepath.Join(env.WorkDir, "home")
			env.Setenv("HOME", home)
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
			env.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
			env.Setenv("UPD_TEST_ROOT", filepath.Join(env.WorkDir, "root"))
			env.Setenv("NO_COLOR", "1")
			return nil
		},
	})
}

func TestPrecheckSudoSkips(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell commands")
	}
	build := func(t *testing.T, doc string, opts plan.Options) (*plan.Plan, *run.Runner) {
		t.Helper()
		f, err := config.Parse("config.toml", []byte(doc))
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := config.Merge(&config.File{Name: "builtin.toml", Jobs: map[string]*config.Spec{}}, f)
		if err != nil {
			t.Fatal(err)
		}
		facts := platform.Facts{GOOS: "linux", Home: t.TempDir()}
		p, err := plan.Build(cfg, facts, opts)
		if err != nil {
			t.Fatal(err)
		}
		return p, &run.Runner{Config: cfg, Plan: p, Facts: facts, LogDir: t.TempDir()}
	}

	t.Run("partial skip keeps sudo only on non-skipped job", func(t *testing.T) {
		p, r := build(t, `
[commands.a]
sudo = true
skip_if = "echo busy"
run = "true"
[commands.b]
sudo = true
skip_if = "exit 1"
run = "true"
`, plan.Options{})
		precheckSudoSkips(context.Background(), r, p.Selected())
		if a := p.Entries["a"]; a.Sudo || !a.SkipChecked || a.SkipReason != "busy" {
			t.Errorf("a = %+v", a)
		}
		if b := p.Entries["b"]; !b.Sudo || !b.SkipChecked || b.SkipReason != "" {
			t.Errorf("b = %+v", b)
		}
	})

	t.Run("bails out when any sudo job lacks skip_if", func(t *testing.T) {
		p, r := build(t, `
[commands.a]
sudo = true
skip_if = "echo busy"
run = "true"
[commands.b]
sudo = true
run = "true"
`, plan.Options{})
		precheckSudoSkips(context.Background(), r, p.Selected())
		if a := p.Entries["a"]; !a.Sudo || a.SkipChecked {
			t.Errorf("a should not have been prechecked: %+v", a)
		}
	})

	t.Run("bails out only when after dependency is selected", func(t *testing.T) {
		doc := `
[commands.dep]
run = "true"
[commands.sub]
after = ["dep"]
sudo = true
skip_if = "echo busy"
run = "true"
`
		p1, r1 := build(t, doc, plan.Options{})
		precheckSudoSkips(context.Background(), r1, p1.Selected())
		if sub := p1.Entries["sub"]; !sub.Sudo || sub.SkipChecked {
			t.Errorf("sub with selected dep should not be prechecked: %+v", sub)
		}

		p2, r2 := build(t, doc, plan.Options{Names: []string{"sub"}})
		precheckSudoSkips(context.Background(), r2, p2.Selected())
		if sub := p2.Entries["sub"]; sub.Sudo || !sub.SkipChecked || sub.SkipReason != "busy" {
			t.Errorf("sub without selected dep should be prechecked: %+v", sub)
		}
	})

	t.Run("cancelled context leaves entries unchanged", func(t *testing.T) {
		p, r := build(t, `
[commands.a]
sudo = true
skip_if = "echo busy"
run = "true"
`, plan.Options{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		precheckSudoSkips(ctx, r, p.Selected())
		if a := p.Entries["a"]; !a.Sudo || a.SkipChecked || a.SkipReason != "" {
			t.Errorf("cancelled ctx mutated a: %+v", a)
		}
	})
}
