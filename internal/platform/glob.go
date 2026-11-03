package platform

import (
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var (
	globMu    sync.Mutex
	globCache = map[string]*regexp.Regexp{}
)

// MatchPath reports whether path matches glob, where * matches any run of
// characters including path separators and ? matches one character.
// Paths are compared with forward slashes, and case-insensitively when
// windows is true.
func MatchPath(glob, path string, windows bool) bool {
	path = filepath.ToSlash(path)
	glob = filepath.ToSlash(glob)
	if windows {
		path = strings.ToLower(strings.ReplaceAll(path, `\`, "/"))
		glob = strings.ToLower(strings.ReplaceAll(glob, `\`, "/"))
	}
	globMu.Lock()
	re, ok := globCache[glob]
	if !ok {
		var b strings.Builder
		b.WriteString("^")
		for _, r := range glob {
			switch r {
			case '*':
				b.WriteString(".*")
			case '?':
				b.WriteString(".")
			default:
				b.WriteString(regexp.QuoteMeta(string(r)))
			}
		}
		b.WriteString("$")
		re = regexp.MustCompile(b.String())
		globCache[glob] = re
	}
	globMu.Unlock()
	return re.MatchString(path)
}
