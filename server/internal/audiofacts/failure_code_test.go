package audiofacts

import (
	"errors"
	"fmt"
	"testing"
)

// NEW-34: a file that changed since its scan is stored and logged as a changed
// source, not as an unreadable file.
func TestFailureCodes(t *testing.T) {
	for err, want := range map[error]string{
		ErrNoAudio:       "no_audio",
		ErrInconsistent:  "inconsistent",
		ErrTooLong:       "too_long",
		ErrSourceChanged: "source_changed",
		fmt.Errorf("wrapped: %w", ErrSourceChanged): "source_changed",
		errors.New("ffprobe failed"):                "unreadable",
	} {
		if got := code(err); got != want {
			t.Errorf("code(%v) = %q, want %q", err, got, want)
		}
	}
}
