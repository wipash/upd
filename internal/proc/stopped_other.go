//go:build !linux

package proc

// Stopped is only implemented on Linux.
func (p *Process) Stopped() bool { return false }
