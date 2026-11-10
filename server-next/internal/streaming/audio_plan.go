package streaming

import (
	"errors"
	"strings"
	"unicode"
)

var ErrConversionCapacity = errors.New("Media conversion capacity is in use. Wait for cleanup or stop another stream, then retry.")

const audioArtifactBytes int64 = 4 << 30
const audioArtifactFiles = 4096

func publicAudioText(s string, n int) string {
	r := []rune(strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)))
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}

// Policy v1 reserves worst-case rate plus25% mux/overshoot allowance,30s timing slack and1MiB playlists.
