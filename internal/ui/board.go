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
	w       io.Writer
	style   Style
	size    func() (width, height int)
	version string
	start   time.Time
	now     func() time.Time

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
func NewBoard(w io.Writer, style Style, size func() (int, int), version string, names, watchers []string) *Board {
	b := &Board{
		w:       w,
		style:   style,
		size:    size,
		version: version,
		start:   time.Now(),
		now:     time.Now,
		rows:    map[string]*row{},
		names:   slices.Sorted(slices.Values(names)),
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
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

	header := b.renderHeader(width, height, visible)

	maxRows := max(height-1-len(header), 1)
	if len(header) == 0 {
		maxRows = max(height-1, 3)
	}
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

	out := make([]line, 0, len(header)+len(visible)+1)
	out = append(out, header...)
	for _, r := range visible {
		out = append(out, b.renderRow(r, nameW, width))
	}
	if folded > 0 {
		t := truncate(fmt.Sprintf("  … %d more", folded), width-1)
		out = append(out, line{b.style.Dim(t), cols(t)})
	}
	return out
}

// renderHeader returns the live logo and progress header: a 3-row stacked
// chevron mark plus a blank line when there is room, a 1-line summary when
// height is tight, or nothing in very small terminals.
func (b *Board) renderHeader(width, height int, visible []*row) []line {
	avail := height - 1
	full := width >= 24 && (avail >= len(visible)+4 || avail >= 12)
	compact := !full && avail >= 4 && width >= 16
	if !full && !compact {
		return nil
	}

	done, failed := 0, 0
	for _, r := range visible {
		if r.result != nil {
			done++
			if r.result.Status.Bad() {
				failed++
			}
		}
	}
	total := len(visible)
	active := done < total
	now := b.now
	if now == nil {
		now = time.Now
	}
	elapsed := formatDuration(max(now().Sub(b.start), 0))
	ver := formatVersion(b.version)

	progressPlain := fmt.Sprintf("%d/%d done · %s", done, total, elapsed)
	progressStyled := b.style.Dim(progressPlain)
	if failed > 0 {
		progressPlain = fmt.Sprintf("%d/%d done · %d failed · %s", done, total, failed, elapsed)
		progressStyled = b.style.Dim(fmt.Sprintf("%d/%d done · ", done, total)) +
			b.style.Red(fmt.Sprintf("%d failed", failed)) +
			b.style.Dim(" · "+elapsed)
	}

	top, mid, bot := b.chevrons(active)
	if full {
		maxRight := max(width-1-4, 0)
		titlePlain := "upd" + ver
		titleStyled := b.style.Bold("upd") + b.style.Dim(ver)
		if cols(titlePlain) > maxRight {
			titlePlain = truncate(titlePlain, maxRight)
			titleStyled = b.style.Bold(titlePlain)
		}
		progPlain, progStyled := progressPlain, progressStyled
		if cols(progPlain) > maxRight {
			progPlain = truncate(progPlain, maxRight)
			progStyled = b.style.Dim(progPlain)
		}
		return []line{
			{top, 2},
			{mid + "  " + titleStyled, 4 + cols(titlePlain)},
			{bot + "  " + progStyled, 4 + cols(progPlain)},
			{"", 0},
		}
	}

	// Compact 1-line header for short terminals.
	chev := top
	if active {
		switch (b.frame / 3) % 4 {
		case 0:
			chev = bot
		case 1:
			chev = mid
		}
	}
	maxRight := max(width-1-3, 0)
	rightPlain := "upd" + ver + " · " + progressPlain
	rightStyled := b.style.Bold("upd") + b.style.Dim(ver+" · ") + progressStyled
	if cols(rightPlain) > maxRight {
		rightPlain = truncate(rightPlain, maxRight)
		rightStyled = b.style.Dim(rightPlain)
	}
	return []line{
		{chev + " " + rightStyled, 3 + cols(rightPlain)},
	}
}

// chevrons returns the three stacked logo rows (top to bottom). While jobs are
// running, a pulse sweeps upward from the bottom chevron to the top.
func (b *Board) chevrons(active bool) (top, mid, bot string) {
	const mark = "╱╲"
	top = b.style.Green(mark)
	mid = b.style.Cyan(mark)
	bot = b.style.DimCyan(mark)
	if !active {
		return top, mid, bot
	}
	switch (b.frame / 3) % 4 {
	case 0:
		bot = b.style.BoldCyan(mark)
	case 1:
		mid = b.style.BoldCyan(mark)
	case 2:
		top = b.style.BoldGreen(mark)
	}
	return top, mid, bot
}

func formatVersion(v string) string {
	if v == "" {
		return ""
	}
	if v == "dev" || strings.HasPrefix(v, "v") {
		return " " + v
	}
	return " v" + v
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
