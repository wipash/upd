//go:build windows

package proc

import (
	"os"
	"syscall"
)

// Windows support is limited to starting and killing the direct child
// until Job Objects are added.
func sysProcAttr(bool) *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

func (p *Process) Interrupt() { _ = p.cmd.Process.Kill() }
func (p *Process) Terminate() { _ = p.cmd.Process.Kill() }
func (p *Process) Kill()      { _ = p.cmd.Process.Kill() }

func killGroup(_ int, p *os.Process) { _ = p.Kill() }
