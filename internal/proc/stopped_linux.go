package proc

import (
	"os"
	"strconv"
	"strings"
)

// Stopped reports whether any process in the process group is stopped,
// which happens when a process in a background group tries to read from
// or configure the terminal.
func (p *Process) Stopped() bool {
	pgid := p.cmd.Process.Pid
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, e := range entries {
		if _, err := strconv.Atoi(e.Name()); err != nil {
			continue
		}
		data, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// Fields after the parenthesised command name: state ppid pgrp ...
		s := string(data)
		i := strings.LastIndexByte(s, ')')
		if i < 0 {
			continue
		}
		f := strings.Fields(s[i+1:])
		if len(f) < 3 || f[2] != strconv.Itoa(pgid) {
			continue
		}
		if f[0] == "T" {
			return true
		}
	}
	return false
}
