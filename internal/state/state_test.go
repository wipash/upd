package state

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLockExcludesSecondHolder(t *testing.T) {
	dir := t.TempDir()
	l, err := Acquire(dir)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Acquire(dir)
	var locked *ErrLocked
	if !errors.As(err, &locked) || locked.PID != os.Getpid() {
		t.Fatalf("second Acquire: %v", err)
	}
	l.Release()
	l2, err := Acquire(dir)
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	l2.Release()
}

func TestNewRunDirPrunes(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var last string
	for i := range 13 {
		var err error
		if last, err = NewRunDir(dir, base.Add(time.Duration(i)*time.Second), 10); err != nil {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "logs"))
	if len(entries) != 10 {
		t.Errorf("%d run dirs kept, want 10", len(entries))
	}
	if _, err := os.Stat(last); err != nil {
		t.Errorf("newest run dir was pruned: %v", err)
	}
}
