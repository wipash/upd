package platform

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestOSRelease(t *testing.T) {
	p := filepath.Join(t.TempDir(), "os-release")
	os.WriteFile(p, []byte("NAME=\"Bazzite\"\nID=bazzite\nID_LIKE=\"fedora rhel\"\n"), 0o644)
	if got := osRelease(p); !slices.Equal(got, []string{"bazzite", "fedora", "rhel"}) {
		t.Errorf("got %v", got)
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		glob, path string
		windows    bool
		want       bool
	}{
		{"*/Cellar/*", "/home/linuxbrew/.linuxbrew/Cellar/herdr/0.9.5/bin/herdr", false, true},
		{"/usr/bin/*", "/usr/local/bin/aws", false, false},
		{"/usr/local/aws-cli/*", "/usr/local/aws-cli/v2/2.39.3/bin/aws", false, true},
		{"*/WinGet/Packages/*", `C:\Users\s\AppData\Local\Microsoft\WinGet\Packages\x\a.exe`, true, true},
		{"*/winget/packages/*", `C:\Users\s\AppData\Local\Microsoft\WinGet\Packages\x\a.exe`, false, false},
		{"/opt/a?c/*", "/opt/abc/x", false, true},
		{"/opt/a.c/*", "/opt/abc/x", false, false},
	}
	for _, tt := range tests {
		if got := MatchPath(tt.glob, tt.path, tt.windows); got != tt.want {
			t.Errorf("MatchPath(%q, %q) = %v", tt.glob, tt.path, got)
		}
	}
}
