package metadata

import "time"

// SetPictureTimeout makes a picture's download give up after d, for a test of a picture host
// that never answers, and returns what puts it back.
func SetPictureTimeout(d time.Duration) (restore func()) {
	saved := pictureTimeout
	pictureTimeout = d
	return func() { pictureTimeout = saved }
}
