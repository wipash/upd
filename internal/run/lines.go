package run

import (
	"bytes"
	"strings"
	"unicode/utf8"
)

// lineBuffer turns raw program output into the lines a terminal would end
// up showing: escape sequences are removed, a carriage return starts the
// line over, and a backspace deletes a character.
type lineBuffer struct {
	partial []byte
}

// feed adds output and returns the lines it completed.
func (l *lineBuffer) feed(p []byte) []string {
	l.partial = append(l.partial, p...)
	var out []string
	for {
		i := bytes.IndexByte(l.partial, '\n')
		if i < 0 {
			break
		}
		out = append(out, render(l.partial[:i]))
		l.partial = l.partial[i+1:]
	}
	// Progress bars redraw with \r and may never end the line. Only the
	// text after the last \r can still show, so drop the rest.
	if i := bytes.LastIndexByte(l.partial[:max(len(l.partial)-1, 0)], '\r'); i >= 0 {
		l.partial = l.partial[i+1:]
	}
	return out
}

// current returns the unfinished line as it would display now.
func (l *lineBuffer) current() string { return render(l.partial) }

func render(b []byte) string {
	var line []rune
	for i := 0; i < len(b); {
		c := b[i]
		switch {
		case c == 0x1b:
			i = skipEscape(b, i)
			continue
		case c == '\r':
			// "\r\n" ends a line normally; a lone \r rewinds it.
			if i+1 < len(b) {
				line = line[:0]
			}
		case c == '\b':
			if len(line) > 0 {
				line = line[:len(line)-1]
			}
		case c == '\t':
			line = append(line, ' ')
		case c < 0x20 || c == 0x7f:
		default:
			r, size := utf8.DecodeRune(b[i:])
			line = append(line, r)
			i += size
			continue
		}
		i++
	}
	return strings.TrimSpace(string(line))
}

// skipEscape returns the index after the escape sequence starting at i.
func skipEscape(b []byte, i int) int {
	i++
	if i >= len(b) {
		return i
	}
	switch b[i] {
	case '[': // CSI: parameters, then a final byte in 0x40-0x7e
		for i++; i < len(b); i++ {
			if b[i] >= 0x40 && b[i] <= 0x7e {
				return i + 1
			}
		}
		return i
	case ']': // OSC: ends with BEL or ESC \
		for i++; i < len(b); i++ {
			if b[i] == 0x07 {
				return i + 1
			}
			if b[i] == 0x1b && i+1 < len(b) && b[i+1] == '\\' {
				return i + 2
			}
		}
		return i
	default:
		return i + 1
	}
}

// Render returns a line of raw output as a terminal would show it.
func Render(b []byte) string { return render(b) }
