package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const polkitTimeout = 3 * time.Second

// polkitTempAuthKey appears in pkcheck stdout when authorisation comes from a
// cached temporary grant (auth_admin_keep / auth_self_keep). Temporary grants
// expire after a few minutes and process-scoped grants do not transfer to
// child processes, so upd only accepts implicit or rule-based authorisations.
const polkitTempAuthKey = `polkit\56temporary_authorization_id=`

type polkitChecker struct {
	mu          sync.Mutex
	cache       map[string]bool
	unavailable bool
	timeout     time.Duration
}

func newPolkitChecker() func(string) bool {
	c := &polkitChecker{cache: map[string]bool{}, timeout: polkitTimeout}
	return c.check
}

func (c *polkitChecker) check(action string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.unavailable {
		return false
	}
	if ok, seen := c.cache[action]; seen {
		return ok
	}

	allowed, unavailable := runPkcheck(action, c.timeout)
	if unavailable {
		c.unavailable = true
	}
	c.cache[action] = allowed
	return allowed
}

func runPkcheck(action string, timeout time.Duration) (allowed, unavailable bool) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "pkcheck", "--action-id", action, "--process", polkitProcess())
	cmd.WaitDelay = time.Second
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err == nil {
		return !strings.Contains(out.String(), polkitTempAuthKey), false
	}
	if errors.Is(err, exec.ErrNotFound) || ctx.Err() != nil {
		return false, true
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 126 {
		// Exit 126 means pkcheck could not reach the polkit authority (for
		// example, no system D-Bus socket), so further probes will also fail.
		return false, true
	}
	return false, false
}

// polkitProcess formats this process for pkcheck --process. Including the
// start time and UID avoids PID-reuse races and pkcheck's /proc lookup.
func polkitProcess() string {
	pid := os.Getpid()
	if start, ok := procStartTime("/proc/self/stat"); ok {
		return fmt.Sprintf("%d,%s,%d", pid, start, os.Geteuid())
	}
	return strconv.Itoa(pid)
}

func procStartTime(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return parseStatStartTime(string(data))
}

func parseStatStartTime(stat string) (string, bool) {
	i := strings.LastIndexByte(stat, ')')
	if i < 0 || i+1 >= len(stat) {
		return "", false
	}
	// Fields after "(comm)" start at field 3 (state); starttime is field 22.
	fields := strings.Fields(stat[i+1:])
	if len(fields) <= 19 {
		return "", false
	}
	return fields[19], true
}
