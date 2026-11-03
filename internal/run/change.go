package run

import (
	"slices"
	"strconv"
	"strings"
)

// describeChange summarises how version output changed. Short outputs show
// the first differing line as "old → new", with words they share removed;
// longer outputs, such as package lists, compare lines as a set and count
// how many changed.
func describeChange(old, cur string) string {
	ol, nl := splitLines(old), splitLines(cur)
	if len(ol) <= 3 && len(nl) <= 3 {
		for i := 0; i < max(len(ol), len(nl)); i++ {
			a, b := at(ol, i), at(nl, i)
			if a != b {
				return trimShared(a, b)
			}
		}
		return ""
	}
	added, removed := 0, 0
	for _, l := range nl {
		if !slices.Contains(ol, l) {
			added++
		}
	}
	for _, l := range ol {
		if !slices.Contains(nl, l) {
			removed++
		}
	}
	switch {
	case added > 0 && removed > 0:
		return strconv.Itoa(max(added, removed)) + " changed"
	case added > 0:
		return strconv.Itoa(added) + " added"
	case removed > 0:
		return strconv.Itoa(removed) + " removed"
	}
	// Same lines in a different order.
	return ""
}

func splitLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func at(l []string, i int) string {
	if i < len(l) {
		return l[i]
	}
	return "?"
}

// trimShared turns "claude 2.1.317 (Claude Code)" and
// "claude 2.1.320 (Claude Code)" into "2.1.317 → 2.1.320".
func trimShared(a, b string) string {
	aw, bw := strings.Fields(a), strings.Fields(b)
	for len(aw) > 1 && len(bw) > 1 && aw[0] == bw[0] {
		aw, bw = aw[1:], bw[1:]
	}
	for len(aw) > 1 && len(bw) > 1 && aw[len(aw)-1] == bw[len(bw)-1] {
		aw, bw = aw[:len(aw)-1], bw[:len(bw)-1]
	}
	return strings.Join(aw, " ") + " → " + strings.Join(bw, " ")
}

// firstLine returns the first non-empty line of s.
func firstLine(s string) string {
	if l := splitLines(s); len(l) > 0 {
		return l[0]
	}
	return ""
}
