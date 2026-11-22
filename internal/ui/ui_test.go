package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/wipash/upd/internal/run"
)

func TestTruncateCountsColumns(t *testing.T) {
	s := truncate("下载完成 downloading", 8)
	if got := cols(s); got > 8 {
		t.Errorf("truncate gave %q, %d columns", s, got)
	}
	if got := pad("ab", 5); got != "ab   " {
		t.Errorf("pad = %q", got)
	}
}

func testBoard(style Style, version string, names ...string) *Board {
	start := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	rows := map[string]*row{}
	for _, n := range names {
		rows[n] = &row{name: n, status: run.Running, start: start}
	}
	return &Board{
		style:   style,
		version: version,
		start:   start,
		now:     func() time.Time { return start.Add(2500 * time.Millisecond) },
		rows:    rows,
		names:   names,
	}
}

func TestBoardHeaderFullAndCompact(t *testing.T) {
	b := testBoard(Style{Color: false}, "0.3.2", "brew", "flatpak", "uv")
	b.rows["brew"].result = &run.Result{Name: "brew", Status: run.Updated, Detail: "3 packages"}
	b.rows["uv"].result = &run.Result{Name: "uv", Status: run.Failed, Detail: "exit 1"}

	// Standard terminal: 3 chevron rows + blank separator + 3 jobs = 7 lines.
	lines := b.render(80, 24)
	if len(lines) != 7 {
		t.Fatalf("len(lines) = %d, want 7", len(lines))
	}
	if lines[0].text != "╱╲" || lines[0].width != 2 {
		t.Errorf("line 0 = %q (w=%d), want ╱╲ (w=2)", lines[0].text, lines[0].width)
	}
	if lines[1].text != "╱╲  upd v0.3.2" {
		t.Errorf("line 1 = %q, want %q", lines[1].text, "╱╲  upd v0.3.2")
	}
	if lines[2].text != "╱╲  2/3 done · 1 failed · 2.5s" {
		t.Errorf("line 2 = %q, want %q", lines[2].text, "╱╲  2/3 done · 1 failed · 2.5s")
	}
	if lines[3].text != "" || lines[3].width != 0 {
		t.Errorf("line 3 = %q (w=%d), want empty separator", lines[3].text, lines[3].width)
	}

	// eraseSeq counts the blank separator row too, so closing erases all 7 rows.
	b.prev = make([]int, len(lines))
	for i, l := range lines {
		b.prev[i] = l.width
	}
	if got := b.eraseSeq(80); got != "\x1b[7F\x1b[J" {
		t.Errorf("eraseSeq = %q, want %q", got, "\x1b[7F\x1b[J")
	}

	// Tight terminal (6 rows -> 5 available): collapses to 1-line header + 3 jobs = 4 lines.
	compact := b.render(80, 6)
	if len(compact) != 4 {
		t.Fatalf("compact len(lines) = %d, want 4", len(compact))
	}
	if compact[0].text != "╱╲ upd v0.3.2 · 2/3 done · 1 failed · 2.5s" {
		t.Errorf("compact header = %q", compact[0].text)
	}

	// Ultra-short terminal (3 rows -> 2 available): skips header so jobs stay visible.
	tiny := b.render(80, 3)
	if strings.HasPrefix(tiny[0].text, "╱╲") {
		t.Errorf("tiny terminal should omit header, got %q", tiny[0].text)
	}
}

func TestBoardHeaderPulseAndNoColor(t *testing.T) {
	b := testBoard(Style{Color: true}, "dev", "brew")

	// While running, frames step the pulse from bottom -> middle -> top -> rest.
	var seen []string
	for _, f := range []int{0, 3, 6, 9} {
		b.frame = f
		lines := b.render(80, 24)
		sig := lines[0].text + "|" + lines[1].text + "|" + lines[2].text
		seen = append(seen, sig)
	}
	if seen[0] == seen[1] || seen[1] == seen[2] || seen[2] == seen[3] {
		t.Errorf("expected distinct pulse phases across frames, got %v", seen)
	}
	// Bottom chevron is bold cyan at phase 0, top chevron is bold green at phase 2.
	b.frame = 0
	if !strings.HasPrefix(b.render(80, 24)[2].text, "\x1b[1;36m╱╲\x1b[0m") {
		t.Errorf("phase 0 bottom chevron = %q", b.render(80, 24)[2].text)
	}
	b.frame = 6
	if b.render(80, 24)[0].text != "\x1b[1;32m╱╲\x1b[0m" {
		t.Errorf("phase 2 top chevron = %q", b.render(80, 24)[0].text)
	}

	// Once all jobs finish, the logo settles on its resting palette regardless of frame.
	b.rows["brew"].result = &run.Result{Name: "brew", Status: run.UpToDate}
	b.frame = 0
	settled := b.render(80, 24)
	if settled[0].text != "\x1b[32m╱╲\x1b[0m" || !strings.HasPrefix(settled[1].text, "\x1b[36m╱╲\x1b[0m") || !strings.HasPrefix(settled[2].text, "\x1b[2;36m╱╲\x1b[0m") {
		t.Errorf("settled chevrons = %q / %q / %q", settled[0].text, settled[1].text, settled[2].text)
	}

	// NO_COLOR emits zero escape codes.
	plain := testBoard(Style{Color: false}, "v0.3.2", "brew")
	for i, l := range plain.render(80, 24) {
		if strings.Contains(l.text, "\x1b") {
			t.Errorf("plain line %d contains ANSI escape: %q", i, l.text)
		}
	}
}

func TestBoardHeaderNarrowTruncation(t *testing.T) {
	b := testBoard(Style{Color: false}, "0.3.2", "brew", "flatpak")
	visible := []*row{b.rows["brew"], b.rows["flatpak"]}
	for _, width := range []int{16, 20, 24, 30} {
		for _, height := range []int{6, 24} {
			for i, l := range b.renderHeader(width, height, visible) {
				if l.width > width-1 {
					t.Errorf("w=%d h=%d line %d width %d exceeds %d: %q", width, height, i, l.width, width-1, l.text)
				}
				if got := cols(l.text); got != l.width {
					t.Errorf("w=%d h=%d line %d reported width %d != cols %d: %q", width, height, i, l.width, got, l.text)
				}
			}
		}
	}
}

func TestFormatVersion(t *testing.T) {
	cases := map[string]string{
		"":       "",
		"dev":    " dev",
		"v0.3.2": " v0.3.2",
		"0.3.2":  " v0.3.2",
	}
	for in, want := range cases {
		if got := formatVersion(in); got != want {
			t.Errorf("formatVersion(%q) = %q, want %q", in, got, want)
		}
	}
}
