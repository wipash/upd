// Package ui renders job progress and the end-of-run summary.
package ui

import (
	"fmt"
	"time"

	"github.com/mattn/go-runewidth"

	"github.com/wipash/upd/internal/run"
)

// Style controls colour.
type Style struct{ Color bool }

func (s Style) wrap(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\x1b[" + code + "m" + text + "\x1b[0m"
}

func (s Style) Red(t string) string    { return s.wrap("31", t) }
func (s Style) Green(t string) string  { return s.wrap("32", t) }
func (s Style) Yellow(t string) string { return s.wrap("33", t) }
func (s Style) Cyan(t string) string   { return s.wrap("36", t) }
func (s Style) Dim(t string) string    { return s.wrap("2", t) }
func (s Style) Bold(t string) string   { return s.wrap("1", t) }

// mark returns the symbol for a finished status.
func (s Style) mark(st run.Status) string {
	switch st {
	case run.Updated:
		return s.Green("↑")
	case run.Done, run.UpToDate:
		return s.Green("✓")
	case run.Skipped, run.NotRun:
		return s.Yellow("-")
	default:
		return s.Red("✗")
	}
}

func formatDuration(d time.Duration) string {
	switch {
	case d < 10*time.Second:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	default:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
}

// truncate cuts s to at most n terminal columns.
func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	return runewidth.Truncate(s, n, "…")
}

// pad fills s with spaces to n terminal columns.
func pad(s string, n int) string { return runewidth.FillRight(s, n) }

// cols returns the number of terminal columns s takes.
func cols(s string) int { return runewidth.StringWidth(s) }

// detail describes a finished job for a status line.
func detail(r *run.Result) string {
	switch r.Status {
	case run.UpToDate:
		if r.Detail != "" {
			return "up to date (" + r.Detail + ")"
		}
		return "up to date"
	case run.Skipped:
		return "skipped: " + r.Detail
	case run.NotRun:
		return "not run: " + r.Detail
	case run.Failed, run.TimedOut, run.Blocked:
		return r.Detail
	case run.ChangeFailed:
		return r.Detail
	}
	return r.Detail
}
