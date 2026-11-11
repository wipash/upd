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
	for _, scenario := range []string{"approved", "unapproved-dependency", "broken-pnpm", "old-npm", "mixed-failures"} {
		t.Run(scenario, func(t *testing.T) {
			dir := t.TempDir()
			stub := `#!/bin/sh
set -eu
blocked() {
  echo 'npm error code ESTRICTALLOWSCRIPTS' >&2
  echo 'npm error --strict-allow-scripts: some package(s) have install scripts not covered by allowScripts:' >&2
  for dep in "$@"; do echo "npm error   $dep (install: node-gyp rebuild; postinstall: node x.js)" >&2; done
  echo 'npm error Allow them with --allow-scripts' >&2
  echo "error: Could not install package '$tool@latest'" >&2
  echo 'Please confirm the package is valid and run with --verbose for more diagnostics.' >&2
  exit 1
}
case "$*" in
  'list --format plain')
    printf '%s\n' 'runtime node@24.1.0 (default)' 'package-manager npm@12.2.2 (default)' 'package @example/cli@1.0.0 / cli / node@24.1.0 (default)' 'package pnpm@12.18.0 / pnpm / node@24.1.0 (default)'
    ;;
  'run -- npm config get allow-scripts')
    case "$SCENARIO" in old-npm|mixed-failures) echo undefined ;; *) echo approved-helper ;; esac
    ;;
  'install node@24') ;;
  'install --verbose npm@latest'|'install --verbose @example/cli@latest'|'install --verbose pnpm@latest')
    echo '[verbose] noise'
    tool=${3%@latest}
    if [ "$SCENARIO" = mixed-failures ]; then
      case "$tool" in
        npm) blocked shared-dep@1.0.0 ;;
        @example/cli) blocked @scope/native@2.1.0 shared-dep@1.0.0 ;;
        pnpm) echo 'npm error code E404' >&2; exit 1 ;;
      esac
    fi
    if [ "$tool" = npm ]; then exit 0; fi
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
    if [ "$SCENARIO" = unapproved-dependency ] && [ "$tool" = @example/cli ]; then
      if [ "${npm_config_strict_allow_scripts-}" = true ]; then
        blocked @scope/native@2.1.0 approved-helper@1.0.0
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
			wantFailure := scenario != "approved" && scenario != "old-npm"
			if (err != nil) != wantFailure {
				t.Fatalf("error = %v, want failure %v; output: %s", err, wantFailure, out)
			}
			wantInstalled := map[string]string{
				"approved":              "@example/cli\npnpm\n",
				"old-npm":               "@example/cli\npnpm\n",
				"unapproved-dependency": "pnpm\n",
				"broken-pnpm":           "@example/cli\n",
				"mixed-failures":        "",
			}[scenario]
			installed, _ := os.ReadFile(filepath.Join(dir, "installed"))
			if string(installed) != wantInstalled {
				t.Fatalf("installed tools = %q, want %q; output: %s", installed, wantInstalled, out)
			}
			if strings.Contains(string(out), "[verbose]") {
				t.Fatalf("output contains Volta's verbose lines: %s", out)
			}
			wantLast := map[string]string{
				"unapproved-dependency": "install scripts need approval for: @example/cli",
				"broken-pnpm":           "failed to update: pnpm",
				"mixed-failures":        "install scripts need approval for: npm @example/cli; failed to update: pnpm",
			}[scenario]
			if wantFailure {
				if !strings.HasSuffix(strings.TrimSpace(string(out)), wantLast) {
					t.Fatalf("output does not end with %q: %s", wantLast, out)
				}
			}
			wantHint := map[string]string{
				"unapproved-dependency": "  @scope/native\n  approved-helper\n" +
					"To allow them, keeping your existing approvals, run:\n" +
					"  npm config set allow-scripts=approved-helper,@scope/native --location=user\n",
				"mixed-failures": "  shared-dep\n  @scope/native\n" +
					"To allow them, keeping your existing approvals, run:\n" +
					"  npm config set allow-scripts=shared-dep,@scope/native --location=user\n",
			}[scenario]
			if wantHint != "" {
				for _, want := range []string{
					"npm error   @scope/native@2.1.0 (install: node-gyp rebuild; postinstall: node x.js)",
					"Some packages depend on install scripts that npm has not been allowed to run:\n" + wantHint,
				} {
					if !strings.Contains(string(out), want) {
						t.Fatalf("output is missing %q: %s", want, out)
					}
				}
				for _, unwanted := range []string{"confirm the package is valid", "Allow them with"} {
					if strings.Contains(string(out), unwanted) {
						t.Fatalf("output contains misleading advice %q: %s", unwanted, out)
					}
				}
			}
		})
	}
}
