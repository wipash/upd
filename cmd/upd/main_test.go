package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
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
