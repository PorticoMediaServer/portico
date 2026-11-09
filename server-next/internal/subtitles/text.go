package subtitles

import "errors"

const (
	MaxInputBytes       = 4 << 20
	MaxCues             = 20000
	MaxCueBytes         = 8192
	MaxDurationMS int64 = 24 * 60 * 60 * 1000
)

var (
	ErrCapacity = errors.New("subtitle capacity exceeded")
	ErrText     = errors.New("unsupported subtitle text")
	ErrTiming   = errors.New("unsupported subtitle timing")
)
