// Package state manages upd's state directory: the lock that keeps two
// runs apart, and the per-run log directories.
package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Dir returns the state directory.
func Dir() (string, error) {
	if x := os.Getenv("XDG_STATE_HOME"); x != "" && filepath.IsAbs(x) {
		return filepath.Join(x, "upd"), nil
	}
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "upd"), nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "upd"), nil
}

// ErrLocked means another upd run holds the lock.
type ErrLocked struct{ PID int }

func (e *ErrLocked) Error() string {
	if e.PID > 0 {
		return fmt.Sprintf("another upd run is in progress (pid %d)", e.PID)
	}
	return "another upd run is in progress"
}

// Lock is the single-instance lock.
type Lock struct{ f *os.File }

// Acquire takes the lock in dir without waiting.
func Acquire(dir string) (*Lock, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "lock"), os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}
	if err := tryLock(f); err != nil {
		data, _ := os.ReadFile(f.Name())
		f.Close()
		pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
		return nil, &ErrLocked{PID: pid}
	}
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return &Lock{f: f}, nil
}

// Release releases the lock.
func (l *Lock) Release() {
	_ = l.f.Truncate(0)
	l.f.Close()
}

// NewRunDir creates the log directory for a run and prunes old ones so
// that keep directories remain. Call it while holding the lock.
func NewRunDir(dir string, now time.Time, keep int) (string, error) {
	logs := filepath.Join(dir, "logs")
	run := filepath.Join(logs, fmt.Sprintf("%s-%d", now.Format("20060102-150405"), os.Getpid()))
	if err := os.MkdirAll(run, 0o755); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(logs)
	if err != nil {
		return run, nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	slices.Sort(names)
	var errs []error
	for len(names) > keep {
		if err := os.RemoveAll(filepath.Join(logs, names[0])); err != nil {
			errs = append(errs, err)
		}
		names = names[1:]
	}
	return run, errors.Join(errs...)
}
