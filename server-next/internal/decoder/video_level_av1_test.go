package decoder

import "testing"

// The levels are the ones libsvtav1 was seen to write for these pictures.
func TestAV1OutputLevelIsTheOneTheEncoderWrites(t *testing.T) {
	for _, c := range []struct {
		width, height int
		rate          float64
		level         int
	}{{426, 240, 24, 0}, {854, 480, 24, 4}, {1280, 720, 24, 5}, {1920, 1080, 24, 8}, {1920, 1080, 60, 9}, {3840, 2160, 24, 12}} {
		if got := AV1OutputLevel(c.width, c.height, c.rate); got != c.level {
			t.Errorf("%dx%d at %v: level %d, want %d", c.width, c.height, c.rate, got, c.level)
		}
	}
}
