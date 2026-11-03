//go:build unix

package proc

import (
	"os"
	"syscall"
)

func sysProcAttr(terminal bool) *syscall.SysProcAttr {
	if terminal {
		return &syscall.SysProcAttr{Setpgid: true}
	}
	return &syscall.SysProcAttr{Setsid: true}
}

// Signal sends sig to the process's whole group.
func (p *Process) signal(sig syscall.Signal) {
	_ = syscall.Kill(-p.cmd.Process.Pid, sig)
}

// Interrupt asks the process group to stop, as Ctrl-C would.
func (p *Process) Interrupt() { p.signal(syscall.SIGINT) }

// Terminate asks the process group to exit. Stopped processes are resumed
// so they can act on it.
func (p *Process) Terminate() {
	p.signal(syscall.SIGTERM)
	p.signal(syscall.SIGCONT)
}

// Kill kills the process group. It has no effect on processes owned by
// another user, such as the commands sudo runs.
func (p *Process) Kill() { p.signal(syscall.SIGKILL) }

func killGroup(pid int, _ *os.Process) { _ = syscall.Kill(-pid, syscall.SIGKILL) }
