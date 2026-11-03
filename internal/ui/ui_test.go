package ui

import "testing"

func TestTruncateCountsColumns(t *testing.T) {
	s := truncate("下载完成 downloading", 8)
	if got := cols(s); got > 8 {
		t.Errorf("truncate gave %q, %d columns", s, got)
	}
	if got := pad("ab", 5); got != "ab   " {
		t.Errorf("pad = %q", got)
	}
}
