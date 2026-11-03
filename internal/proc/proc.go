// Package proc starts job processes so they can be interrupted, timed out
// and killed together with everything they start.
package proc

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

// Spec describes a process to start.
type Spec struct {
	Argv []string
	Env  []string
	Dir  string
	Out  *os.File // receives stdout and stderr

	// Terminal keeps the controlling terminal. sudo needs it to find the
	// credential cached by the up-front `sudo -v`. Every other job runs in
	// a new session, so a program that tries to prompt fails instead of
	// waiting for input.
	Terminal bool
}

// Process is a started job process.
type Process struct {
	cmd *exec.Cmd
}

// Start starts the process with stdin connected to the null device.
func Start(s Spec) (*Process, error) {
	cmd := exec.Command(s.Argv[0], s.Argv[1:]...)
	cmd.Env = s.Env
	cmd.Dir = s.Dir
	cmd.Stdout = s.Out
	cmd.Stderr = s.Out
	cmd.SysProcAttr = sysProcAttr(s.Terminal)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &Process{cmd: cmd}, nil
}

// Wait waits for the process to exit and returns its exit code. err is set
// only when the process could not be waited for or was killed by a signal.
func (p *Process) Wait() (code int, err error) {
	err = p.cmd.Wait()
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ee.ExitCode() >= 0 {
			return ee.ExitCode(), nil
		}
		return -1, err
	}
	if err != nil {
		return -1, err
	}
	return 0, nil
}

// Pid returns the process ID, which is also its process group ID.
func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Output runs a short command and returns its stdout and exit
// code. stderr is discarded. The process group is killed when ctx ends.
func Output(ctx context.Context, argv, env []string, dir string) (stdout string, code int, err error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.SysProcAttr = sysProcAttr(false)
	cmd.Cancel = func() error { killGroup(cmd.Process.Pid, cmd.Process); return nil }
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	var ee *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return out.String(), -1, ctx.Err()
	case errors.As(err, &ee) && ee.ExitCode() >= 0:
		return out.String(), ee.ExitCode(), nil
	case errors.Is(err, exec.ErrWaitDelay):
		return out.String(), 0, nil
	case err != nil:
		return out.String(), -1, err
	}
	return out.String(), 0, nil
}
