package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the shipped shell job with an installer that, like npm 12,
// succeeds but leaves tools broken when their install scripts are blocked.
func TestVoltaInstallScripts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Volta update job is Unix-only")
	}
	for _, scenario := range []string{"approved", "unapproved-dependency", "broken-pnpm", "old-npm"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			stub := `#!/bin/sh
set -eu
case "$*" in
  'list --format plain')
    printf '%s\n' 'runtime node@24.1.0 (default)' 'package-manager npm@12.2.2 (default)' 'package @example/cli@1.0.0 / cli / node@24.1.0 (default)' 'package pnpm@12.18.0 / pnpm / node@24.1.0 (default)'
    ;;
  'run -- npm config get allow-scripts')
    if [ "$SCENARIO" = old-npm ]; then echo undefined; else echo approved-helper; fi
    ;;
  'install node@24'|'install npm@latest') ;;
  'install @example/cli@latest'|'install pnpm@latest')
    tool=${2%@latest}
    case ",${npm_config_allow_scripts-}," in
      *",$tool,"*) ;;
      *) exit 0 ;; # npm's silent broken installation
    esac
    if [ "$SCENARIO" != old-npm ]; then
      case ",$npm_config_allow_scripts," in
        *,approved-helper,*) ;;
        *) echo 'existing approval lost' >&2; exit 1 ;;
      esac
    fi
    if [ "$SCENARIO" = unapproved-dependency ]; then
      if [ "${npm_config_strict_allow_scripts-}" = true ]; then
        echo 'unapproved dependency install script' >&2
        exit 1
      fi
      exit 0
    fi
    if [ "$tool" = pnpm ] && [ "$SCENARIO" = broken-pnpm ]; then exit 0; fi
    printf '%s\n' "$tool" >> "$INSTALLED"
    ;;
  'run -- pnpm --version')
    if grep -qx pnpm "$INSTALLED"; then echo 12.18.0; else exit 126; fi
    ;;
  *) echo "unexpected command: $*" >&2; exit 1 ;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "volta"), []byte(stub), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
			t.Setenv("SCENARIO", scenario)
			t.Setenv("INSTALLED", filepath.Join(dir, "installed"))
			t.Setenv("npm_config_allow_scripts", "")
			t.Setenv("npm_config_strict_allow_scripts", "")
			job := mustMerge(t, "").Jobs["volta"]
			cmd := exec.Command("sh", "-c", strings.Join(job.Run.For("linux"), "\n"))
			out, err := cmd.CombinedOutput()
			wantFailure := scenario == "unapproved-dependency" || scenario == "broken-pnpm"
			if (err != nil) != wantFailure {
				t.Fatalf("error = %v, want failure %v; output: %s", err, wantFailure, out)
			}
			if !wantFailure {
				installed, err := os.ReadFile(filepath.Join(dir, "installed"))
				if err != nil || string(installed) != "@example/cli\npnpm\n" {
					t.Fatalf("installed tools = %q (%v); output: %s", installed, err, out)
				}
			}
		})
	}
}
