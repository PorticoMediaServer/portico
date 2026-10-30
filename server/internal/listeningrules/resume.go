package listeningrules

import (
	"errors"
	"math"
)

var ErrResumeRewind = errors.New("invalid audiobook resume rewind")

// Resume applies the profile's sole automatic rewind to an actual stored
// resume. Callers must not apply it to an explicit position or continuation.
// Elapsed time never selects a second, hidden amount.
func Resume(position float64, rewindMS int) (float64, error) {
	switch rewindMS {
	case 0, 5000, 15000, 30000:
		return math.Max(0, position-float64(rewindMS)/1000), nil
	default:
		return 0, ErrResumeRewind
	}
}
