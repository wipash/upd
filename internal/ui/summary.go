package ui

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/wipash/upd/internal/run"
)

const failureTail = 40

// Summary prints the output of failed jobs, then one line per job, then
// totals. hidden is the number of jobs that do not apply to this machine.
func Summary(w io.Writer, s Style, results []*run.Result, post *run.Result, hidden int, logDir string, width int) {
	var shown []*run.Result
	for _, r := range results {
		if r.Watch && (r.Status == run.UpToDate || r.Status == run.Done || r.Status == run.NotRun) {
			continue
		}
		shown = append(shown, r)
	}
	all := shown
	if post != nil {
		all = append(slices.Clone(shown), post)
	}
	for _, r := range all {
		if r.Status.Bad() {
			printFailure(w, s, r)
		}
	}
	nameW := 0
	for _, r := range all {
		nameW = max(nameW, cols(r.Name))
	}
	counts := map[string]int{}
	for _, r := range all {
		dur := ""
		if !r.Start.IsZero() {
			dur = formatDuration(r.End.Sub(r.Start))
		}
		d := detail(r)
		if r.Status.Bad() {
			d = s.Red(truncate(d, max(width-nameW-14, 10)))
		} else {
			d = truncate(d, max(width-nameW-14, 10))
		}
		fmt.Fprintf(w, "%s %s %7s  %s\n", s.mark(r.Status), pad(r.Name, nameW), dur, d)
		switch {
		case r.Status == run.Updated:
			counts["updated"]++
		case r.Status.Bad():
			counts["failed"]++
		case r.Status == run.Skipped:
			counts["skipped"]++
		case r.Status == run.NotRun:
			counts["not run"]++
		default:
			counts["ok"]++
		}
	}
	var parts []string
	for _, k := range []string{"updated", "ok", "failed", "skipped", "not run"} {
		if n := counts[k]; n > 0 {
			p := fmt.Sprintf("%d %s", n, k)
			if k == "failed" {
				p = s.Red(p)
			}
			parts = append(parts, p)
		}
	}
	if hidden > 0 {
		parts = append(parts, fmt.Sprintf("%d hidden", hidden))
	}
	if len(parts) == 0 {
		parts = append(parts, "nothing to do")
	}
	fmt.Fprintf(w, "\n%s\n", strings.Join(parts, ", "))
	if logDir != "" {
		fmt.Fprintf(w, "%s\n", s.Dim("logs: "+logDir))
	}
}

func printFailure(w io.Writer, s Style, r *run.Result) {
	fmt.Fprintf(w, "%s %s\n", s.Red("━━ "+r.Name+":"), r.Detail)
	data, err := os.ReadFile(r.Log)
	if err != nil {
		fmt.Fprintf(w, "(no log: %v)\n\n", err)
		return
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	for sc.Scan() {
		lines = append(lines, run.Render(sc.Bytes()))
	}
	if len(lines) > failureTail {
		fmt.Fprintf(w, "%s\n", s.Dim(fmt.Sprintf("… %d earlier lines in %s", len(lines)-failureTail, r.Log)))
		lines = lines[len(lines)-failureTail:]
	}
	for _, l := range lines {
		fmt.Fprintf(w, "  %s\n", l)
	}
	fmt.Fprintln(w)
}
