// Package platform describes the machine upd runs on.
package platform

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// Facts are the properties of the machine that job predicates test.
type Facts struct {
	GOOS   string
	Distro []string // os-release ID followed by the words of ID_LIKE
	WSL    bool
	SSH    bool
	Home   string

	// root prefixes absolute paths checked by predicates. It is empty except
	// in tests, which set UPD_TEST_ROOT to fake files such as
	// /etc/os-release or /run/ostree-booted.
	root string
}

// Detect inspects the current machine.
func Detect() Facts {
	f := Facts{GOOS: runtime.GOOS, root: os.Getenv("UPD_TEST_ROOT")}
	f.Home, _ = os.UserHomeDir()
	f.SSH = os.Getenv("SSH_CONNECTION") != "" || os.Getenv("SSH_TTY") != ""
	if f.GOOS == "linux" {
		f.Distro = osRelease(f.Path("/etc/os-release"))
		if len(f.Distro) == 0 {
			f.Distro = osRelease(f.Path("/usr/lib/os-release"))
		}
		f.WSL = os.Getenv("WSL_DISTRO_NAME") != "" || exists(f.Path("/proc/sys/fs/binfmt_misc/WSLInterop"))
	}
	return f
}

func osRelease(path string) []string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var id string
	var like []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok {
			continue
		}
		v = strings.ToLower(strings.Trim(v, `"'`))
		switch k {
		case "ID":
			id = v
		case "ID_LIKE":
			like = strings.Fields(v)
		}
	}
	if id == "" {
		return like
	}
	return append([]string{id}, like...)
}

// Path maps an absolute path from the config onto the filesystem being
// inspected.
func (f Facts) Path(p string) string {
	if f.root == "" || !filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(f.root, p)
}

// Expand expands a leading ~ and $VAR references, then applies Path.
func (f Facts) Expand(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		return filepath.Join(f.Home, p[1:])
	}
	return f.Path(os.ExpandEnv(p))
}

// Exists reports whether a config path exists.
func (f Facts) Exists(p string) bool { return exists(f.Expand(p)) }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Which finds a program on PATH and returns its path with symlinks resolved.
func Which(name string) (found, real string, err error) {
	found, err = exec.LookPath(name)
	if err != nil {
		return "", "", err
	}
	real, err = filepath.EvalSymlinks(found)
	if err != nil {
		real = found
	}
	if abs, err := filepath.Abs(real); err == nil {
		real = abs
	}
	return found, real, nil
}
