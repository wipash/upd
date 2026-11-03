package plan

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/wipash/upd/internal/platform"
)

// Each fixture is a real /etc/os-release. The test checks which system
// package manager job applies, with every package manager installed.
func TestDistroDetection(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os-release is only read on Linux")
	}
	bin := t.TempDir()
	for _, b := range []string{"apt-get", "dnf", "apk", "pacman", "zypper", "rpm-ostree"} {
		fakeBin(t, bin, "", b)
	}
	t.Setenv("PATH", bin)
	cfg := load(t, "")
	want := map[string][]string{
		"alpine":              {"apk"},
		"fedora":              {"dnf"},
		"arch":                {"pacman"},
		"endeavouros":         {"pacman"},
		"debian":              {"apt"},
		"ubuntu":              {"apt"},
		"bazzite":             {"rpm-ostree"},
		"opensuse-tumbleweed": {"zypper-dup"},
		"opensuse-leap":       {"zypper-update"},
	}
	system := []string{"apt", "dnf", "apk", "pacman", "zypper-dup", "zypper-update", "rpm-ostree"}
	for distro, jobs := range want {
		t.Run(distro, func(t *testing.T) {
			root := t.TempDir()
			os.MkdirAll(filepath.Join(root, "etc"), 0o755)
			data, err := os.ReadFile(filepath.Join("testdata", "os-release", distro))
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(root, "etc", "os-release"), data, 0o644)
			if distro == "bazzite" {
				os.MkdirAll(filepath.Join(root, "run"), 0o755)
				os.WriteFile(filepath.Join(root, "run", "ostree-booted"), nil, 0o644)
			}
			t.Setenv("UPD_TEST_ROOT", root)
			t.Setenv("SSH_CONNECTION", "")
			t.Setenv("SSH_TTY", "")
			f := platform.Detect()
			var got []string
			for _, n := range system {
				if Visible(cfg, cfg.Jobs[n], f) == "" {
					got = append(got, n)
				}
			}
			if !slices.Equal(got, jobs) {
				t.Errorf("visible system jobs %v, want %v (distro %v)", got, jobs, f.Distro)
			}
		})
	}
}
