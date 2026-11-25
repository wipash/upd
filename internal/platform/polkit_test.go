package platform

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseStatStartTime(t *testing.T) {
	// Real /proc/self/stat format, including spaces and parentheses inside comm.
	stat := "4210 (my (weird) proc) S 100 4210 100 0 -1 4194304 10 0 0 0 1 2 0 0 20 0 1 0 987654 1000 50"
	got, ok := parseStatStartTime(stat)
	if !ok || got != "987654" {
		t.Fatalf("parseStatStartTime = (%q, %v), want (\"987654\", true)", got, ok)
	}
	for _, bad := range []string{
		"bad stat line",
		"4210 (trailing)",
		"4210 (short) S 1 2 3",
	} {
		if _, ok := parseStatStartTime(bad); ok {
			t.Errorf("parseStatStartTime(%q) unexpectedly succeeded", bad)
		}
	}
	if runtime.GOOS == "linux" {
		if start, ok := procStartTime("/proc/self/stat"); !ok || start == "" {
			t.Errorf("procStartTime(/proc/self/stat) = (%q, %v)", start, ok)
		}
	}
}

func TestPolkitAllowedGuards(t *testing.T) {
	called := false
	f := Facts{GOOS: "darwin", CheckPolkit: func(string) bool { called = true; return true }}
	if f.PolkitAllowed("org.example.op") || called {
		t.Error("PolkitAllowed ran on darwin")
	}
	if (Facts{GOOS: "linux"}).PolkitAllowed("org.example.op") {
		t.Error("nil CheckPolkit should return false")
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func writeFakePkcheck(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pkcheck")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	return dir
}

func readCallLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func TestPolkitChecker(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses POSIX shell script")
	}

	t.Run("allow and memoise without user interaction", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf(`
printf '%%s\n' "$*" >> %s
for arg in "$@"; do
  case "$arg" in -u|--allow-user-interaction) exit 99 ;; esac
done
exit 0
`, shellQuote(logFile)))
		check := newPolkitChecker()
		if !check("org.freedesktop.Flatpak.app-update") {
			t.Fatal("expected allow")
		}
		if !check("org.freedesktop.Flatpak.app-update") {
			t.Fatal("expected cached allow")
		}
		lines := readCallLines(t, logFile)
		if len(lines) != 1 {
			t.Fatalf("pkcheck called %d times, want 1: %v", len(lines), lines)
		}
		if !strings.Contains(lines[0], "--action-id org.freedesktop.Flatpak.app-update") || !strings.Contains(lines[0], "--process ") {
			t.Errorf("unexpected args: %q", lines[0])
		}
	})

	t.Run("reject temporary authorisation and keep checker available", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf(`
printf '%%s\n' "$2" >> %s
case "$2" in
  act.temp)
    printf '%%s\n' 'polkit\56temporary_authorization_id=tmpauthz1'
    exit 0
    ;;
  act.ok) exit 0 ;;
esac
exit 2
`, shellQuote(logFile)))
		check := newPolkitChecker()
		if check("act.temp") {
			t.Fatal("temporary authorisation must not be accepted")
		}
		if check("act.temp") {
			t.Fatal("cached temporary authorisation denial failed")
		}
		if !check("act.ok") {
			t.Fatal("temporary authorisation should not mark checker unavailable")
		}
		lines := readCallLines(t, logFile)
		if len(lines) != 2 {
			t.Fatalf("pkcheck called %d times, want 2: %v", len(lines), lines)
		}
	})

	t.Run("deny exit 1, 2, and 127 without marking unavailable", func(t *testing.T) {
		writeFakePkcheck(t, `
case "$2" in
  act.one) exit 1 ;;
  act.two) exit 2 ;;
  act.missing) exit 127 ;;
  act.ok) exit 0 ;;
esac
exit 2
`)
		check := newPolkitChecker()
		for _, act := range []string{"act.one", "act.two", "act.missing"} {
			if check(act) {
				t.Errorf("%s should deny", act)
			}
		}
		if !check("act.ok") {
			t.Error("exit 1/2/127 should not mark checker unavailable")
		}
	})

	t.Run("exit 126 marks unavailable", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf(`
printf '%%s\n' "$*" >> %s
exit 126
`, shellQuote(logFile)))
		check := newPolkitChecker()
		if check("act.one") {
			t.Error("exit 126 should deny")
		}
		if check("act.two") {
			t.Error("second call after 126 should deny")
		}
		lines := readCallLines(t, logFile)
		if len(lines) != 1 {
			t.Errorf("expected 1 pkcheck call after 126, got %d: %v", len(lines), lines)
		}
	})

	t.Run("missing binary marks unavailable", func(t *testing.T) {
		t.Setenv("PATH", t.TempDir())
		check := newPolkitChecker()
		if check("act.one") {
			t.Error("missing pkcheck should deny")
		}
		// Installing pkcheck after the first miss still short-circuits.
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf("printf '%%s\n' \"$*\" >> %s\nexit 0\n", shellQuote(logFile)))
		if check("act.two") {
			t.Error("checker should stay unavailable after missing pkcheck")
		}
		if _, err := os.Stat(logFile); err == nil {
			t.Error("pkcheck was invoked after being marked unavailable")
		}
	})

	t.Run("timeout marks unavailable", func(t *testing.T) {
		sleepPath, err := exec.LookPath("sleep")
		if err != nil {
			t.Skip("sleep not found")
		}
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf("printf '%%s\n' \"$*\" >> %s\nexec %s 5\n", shellQuote(logFile), shellQuote(sleepPath)))
		c := &polkitChecker{cache: map[string]bool{}, timeout: 150 * time.Millisecond}
		if c.check("act.slow") {
			t.Error("timed-out pkcheck should deny")
		}
		if c.check("act.next") {
			t.Error("second call after timeout should deny")
		}
		lines := readCallLines(t, logFile)
		if len(lines) != 1 {
			t.Errorf("expected 1 pkcheck call after timeout, got %d: %v", len(lines), lines)
		}
	})

	t.Run("concurrent calls are safe and deduplicated", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "calls")
		writeFakePkcheck(t, fmt.Sprintf("printf '%%s\n' \"$2\" >> %s\nexit 0\n", shellQuote(logFile)))
		check := newPolkitChecker()
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if !check("act.shared") {
					t.Error("expected allow")
				}
			}()
		}
		wg.Wait()
		lines := readCallLines(t, logFile)
		if len(lines) != 1 {
			t.Errorf("expected 1 pkcheck call across goroutines, got %d: %v", len(lines), lines)
		}
	})
}
