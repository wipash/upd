package ui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/wipash/upd/internal/run"
)

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const redrawInterval = 100 * time.Millisecond

type row struct {
	name    string
	watch   bool
	status  run.Status
	waiting []string
	start   time.Time
	last    string
	result  *run.Result
}

// Board is a live status display with one line per job, redrawn in place.
type Board struct {
	w     io.Writer
	style Style
	size  func() (width, height int)

	mu      sync.Mutex
	rows    map[string]*row
	names   []string
	prev    []int // display width of each line of the last frame
	frame   int
	closed  sync.Once
	stop    chan struct{}
	stopped chan struct{}
}

// NewBoard starts drawing a board for the given jobs. watchers are only
// shown once they act.
func NewBoard(w io.Writer, style Style, size func() (int, int), names, watchers []string) *Board {
	b := &Board{w: w, style: style, size: size, rows: map[string]*row{}, names: slices.Sorted(slices.Values(names)), stop: make(chan struct{}), stopped: make(chan struct{})}
	for _, n := range names {
		b.rows[n] = &row{name: n, watch: slices.Contains(watchers, n)}
	}
	fmt.Fprint(w, "\x1b[?25l")
	go b.loop()
	return b
}

func (b *Board) loop() {
	defer close(b.stopped)
	t := time.NewTicker(redrawInterval)
	defer t.Stop()
	for {
		b.draw()
		select {
		case <-b.stop:
			return
		case <-t.C:
		}
	}
}

// Close erases the board and restores the cursor. It is safe to call more
// than once and from several goroutines.
func (b *Board) Close() {
	b.closed.Do(func() {
		close(b.stop)
		<-b.stopped
		b.mu.Lock()
		defer b.mu.Unlock()
		b.erase()
		fmt.Fprint(b.w, "\x1b[?25h")
	})
}

func (b *Board) JobWaiting(name string, on []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r := b.rows[name]; r != nil {
		r.status, r.waiting = run.Waiting, on
	}
}

func (b *Board) JobStarted(name string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r := b.rows[name]; r != nil {
		r.status, r.start = run.Running, time.Now()
	}
}

func (b *Board) JobOutput(name string, lines []string, partial string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	r := b.rows[name]
	if r == nil {
		return
	}
	if partial != "" {
		r.last = partial
		return
	}
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i] != "" {
			r.last = lines[i]
			return
		}
	}
}

func (b *Board) JobFinished(res *run.Result) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if r := b.rows[res.Name]; r != nil {
		r.status, r.result = res.Status, res
	}
}

// erase moves the cursor to the start of the last frame and clears it.
// Lines were cut to the width at the time, so after the terminal narrows
// each may now take several rows.
func (b *Board) erase() {
	width, _ := b.size()
	io.WriteString(b.w, b.eraseSeq(width))
	b.prev = nil
}

func (b *Board) eraseSeq(width int) string {
	n := 0
	for _, w := range b.prev {
		n += max(1, (w+width-1)/max(width, 1))
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("\x1b[%dF\x1b[J", n)
}

func (b *Board) draw() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.frame++
	width, height := b.size()
	lines := b.render(width, height)
	var out strings.Builder
	out.WriteString(b.eraseSeq(width))
	b.prev = b.prev[:0]
	for _, l := range lines {
		out.WriteString(l.text)
		out.WriteString("\n")
		b.prev = append(b.prev, l.width)
	}
	io.WriteString(b.w, out.String())
}

type line struct {
	text  string
	width int
}

// render lays out the board. When there are more jobs than rows, finished
// successful jobs fold into a count so running and failed jobs stay visible.
func (b *Board) render(width, height int) []line {
	var visible []*row
	for _, n := range b.names {
		r := b.rows[n]
		if r.watch && (r.result == nil || !(r.status == run.Updated || r.status.Bad())) {
			continue
		}
		visible = append(visible, r)
	}
	nameW := 0
	for _, r := range visible {
		nameW = max(nameW, cols(r.name))
	}
	nameW = min(nameW, max(width/3, 8))

	maxRows := max(height-1, 3)
	folded := 0
	if len(visible) > maxRows {
		// Keep failures, running and waiting jobs; drop finished good ones.
		var keep []*row
		for _, r := range visible {
			if r.result == nil || r.status.Bad() {
				keep = append(keep, r)
			} else {
				folded++
			}
		}
		// Failures first, then running jobs, then jobs still waiting.
		rank := func(r *row) int {
			switch {
			case r.result != nil:
				return 0
			case r.status == run.Running:
				return 1
			}
			return 2
		}
		slices.SortStableFunc(keep, func(a, b *row) int { return rank(a) - rank(b) })
		if len(keep) > maxRows-1 {
			folded += len(keep) - (maxRows - 1)
			keep = keep[:maxRows-1]
		}
		visible = keep
	}

	var out []line
	for _, r := range visible {
		out = append(out, b.renderRow(r, nameW, width))
	}
	if folded > 0 {
		t := truncate(fmt.Sprintf("  … %d more", folded), width-1)
		out = append(out, line{b.style.Dim(t), cols(t)})
	}
	return out
}

func (b *Board) renderRow(r *row, nameW, width int) line {
	var mark, timeText, text string
	switch {
	case r.result != nil:
		mark = b.style.mark(r.status)
		if !r.result.Start.IsZero() {
			timeText = formatDuration(r.result.End.Sub(r.result.Start))
		}
		text = detail(r.result)
	case r.status == run.Running:
		mark = b.style.Cyan(spinner[b.frame%len(spinner)])
		timeText = formatDuration(time.Since(r.start))
		text = r.last
	case r.status == run.Waiting:
		mark = b.style.Dim("·")
		text = "waiting for " + strings.Join(r.waiting, ", ")
	default:
		mark = b.style.Dim("·")
	}
	name := pad(truncate(r.name, nameW), nameW)
	prefix := 2 + nameW + 1 + 7 + 2
	text = truncate(text, width-1-prefix)
	plainWidth := prefix + cols(text)
	styled := text
	switch {
	case r.result != nil && r.status.Bad():
		styled = b.style.Red(text)
	case r.result == nil:
		styled = b.style.Dim(text)
	}
	return line{fmt.Sprintf("%s %s %7s  %s", mark, name, timeText, styled), plainWidth}
}
