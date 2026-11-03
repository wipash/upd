package selfupdate

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewer(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"1.2.0", "1.1.9", true},
		{"1.10.0", "1.9.0", true},
		{"1.2.0", "1.2.0", false},
		{"1.2", "1.2.1", false},
		{"2.0.0-rc1", "1.9.9", true},
	}
	for _, tt := range tests {
		if got := newer(tt.a, tt.b); got != tt.want {
			t.Errorf("newer(%q, %q) = %v", tt.a, tt.b, got)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := []byte("abc123  upd_linux_amd64\ndef456  upd_darwin_arm64\n")
	if got, err := checksumFor(sums, "upd_darwin_arm64"); err != nil || got != "def456" {
		t.Errorf("got %q, %v", got, err)
	}
	if _, err := checksumFor(sums, "upd_linux_arm64"); err == nil {
		t.Error("missing entry did not fail")
	}
}

func TestReplace(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "upd")
	os.WriteFile(exe, []byte("old"), 0o755)
	if err := replace(exe, []byte("new")); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(exe)
	fi, _ := os.Stat(exe)
	if string(data) != "new" || fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("content %q mode %v", data, fi.Mode())
	}
	entries, _ := os.ReadDir(filepath.Dir(exe))
	if len(entries) != 1 {
		t.Errorf("left %d files behind", len(entries))
	}
}
