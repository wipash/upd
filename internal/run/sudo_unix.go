//go:build unix

package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"

	"golang.org/x/term"
)

// sudo caches the password per terminal session, so jobs started with the
// same controlling terminal reuse the credential from the up-front
// `sudo -v` and run with `sudo -n`, which fails instead of prompting.
type sudo struct {
	stop chan struct{}
}

// PrepareElevation asks for the sudo password in the foreground and keeps
// the credential alive until Close.
func PrepareElevation(ctx context.Context) (Elevator, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return nil, errors.New("no terminal to ask for the sudo password")
	}
	if _, err := exec.LookPath("sudo"); err != nil {
		return nil, errors.New("sudo is not installed")
	}
	cmd := exec.CommandContext(ctx, "sudo", "-v", "-p", "[upd] sudo password for %p: ")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, errors.New("sudo was not authorised")
	}
	s := &sudo{stop: make(chan struct{})}
	go s.keepAlive()
	return s, nil
}

func (s *sudo) keepAlive() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.stop:
			return
		case <-t.C:
			_ = exec.Command("sudo", "-n", "-v").Run()
		}
	}
}

// sudo resets the environment, so variables travel through env(1).
func (s *sudo) Wrap(argv []string, env map[string]string) []string {
	out := []string{"sudo", "-n", "--", "env"}
	out = append(out, envPairs(env)...)
	return append(out, argv...)
}

func (s *sudo) Close() { close(s.stop) }
