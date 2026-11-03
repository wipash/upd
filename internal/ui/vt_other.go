//go:build !windows

package ui

import "os"

// EnableVT is a no-op outside Windows, where terminals handle ANSI escapes.
func EnableVT(*os.File) (restore func(), ok bool) { return func() {}, true }
