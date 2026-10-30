//go:build !windows

package artifactlease

import (
	"os"
	"syscall"
)

// SingleLink is required before a crash-recovery writer can change permissions
// or truncate staging. A sealed hard-link must remain immutable.
func SingleLink(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && st.Nlink == 1
}
