//go:build !race

package writer

import "time"

// Normal builds keep the established crash-child budget.
const (
	crashChildTimeout = 25 * time.Second
	crashChildGuard   = 30 * time.Second
	raceDetector      = false
)
