package writer

import (
	"math"
	"strings"
	"unicode/utf8"
)

// basicProbePayloadMaxBytes bounds each probed scalar string independently.
const basicProbePayloadMaxBytes = 4096

// basicProbeObservation is the raw demuxer tuple before payload admission.
// complete distinguishes an observed probe, which may write or clear scalars,
// from an incomplete or unobserved one, which holds no clearing authority.
// Empty container and audio codec, and zero dimensions, are values rather than
// missing facts. No filename or extension fallback belongs here.
type basicProbeObservation struct {
	complete                          bool
	container, videoCodec, audioCodec string
	width, height                     int64
	duration                          float64
}

// prepareBasicProbeScalars admits the raw observation into the exact tuple the
// stage writer consumes. It is private, pure and side-effect free: no SQL, no
// turn, no receipt, no logging. Strings are admitted verbatim (no trimming or
// other normalization) and cloned so the result never aliases the caller's
// buffers. Every refusal returns the zero basicProbeScalars.
func prepareBasicProbeScalars(observed basicProbeObservation) (basicProbeScalars, error) {
	var zero basicProbeScalars
	if !observed.complete {
		return zero, ErrInvalid
	}
	for _, text := range [...]string{observed.container, observed.videoCodec, observed.audioCodec} {
		if len(text) > basicProbePayloadMaxBytes {
			return zero, ErrCap
		}
		if !utf8.ValidString(text) {
			return zero, ErrInvalid
		}
	}
	// A movie needs a video codec. Whitespace is a value, never absence, so
	// only a genuinely empty string is refused.
	if observed.videoCodec == "" {
		return zero, ErrInvalid
	}
	if observed.width < 0 || observed.height < 0 || !(observed.duration > 0) || math.IsNaN(observed.duration) || math.IsInf(observed.duration, 0) {
		return zero, ErrInvalid
	}
	return basicProbeScalars{
		container:  strings.Clone(observed.container),
		videoCodec: strings.Clone(observed.videoCodec),
		audioCodec: strings.Clone(observed.audioCodec),
		width:      observed.width,
		height:     observed.height,
		duration:   observed.duration,
	}, nil
}
