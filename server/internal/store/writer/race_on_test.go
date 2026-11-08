//go:build race

package writer

import "time"

// The race detector multiplies the transpiled SQLite setup in each crash child.
// Scale only the child's own deadline; the parent still requires a witnessed
// SIGKILL and the outer guard stays just beyond it.
const (
	crashChildTimeout = 100 * time.Second
	crashChildGuard   = 130 * time.Second
	raceDetector      = true
)
