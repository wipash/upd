//go:build windows

package ui

import (
	"os"

	"golang.org/x/sys/windows"
)

// EnableVT turns on ANSI escape processing for the console and returns a
// function that restores the previous mode. ok is false when the console
// does not support it.
func EnableVT(f *os.File) (restore func(), ok bool) {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return func() {}, false
	}
	if err := windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING); err != nil {
		return func() {}, false
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }, true
}
