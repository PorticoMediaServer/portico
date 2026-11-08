package writer

import (
	"errors"
	"math"
	"strings"
	"testing"
	"unsafe"
)

func TestBasicProbePayloadAdmitsCompleteTuple(t *testing.T) {
	// A complete mkv/h264 tuple with no audio codec and unknown (zero)
	// dimensions is a value, not a partial observation.
	got, err := prepareBasicProbeScalars(basicProbeObservation{
		complete: true, container: "mkv", videoCodec: "h264", audioCodec: "",
		width: 0, height: 0, duration: 100,
	})
	want := basicProbeScalars{container: "mkv", videoCodec: "h264", audioCodec: "", width: 0, height: 0, duration: 100}
	if err != nil || got != want {
		t.Fatalf("complete tuple must be admitted verbatim: got=%+v err=%v", got, err)
	}
	// An empty container is allowed when the probe is complete.
	if _, err := prepareBasicProbeScalars(basicProbeObservation{complete: true, videoCodec: "h264", duration: 100}); err != nil {
		t.Fatalf("empty container must be admitted: %v", err)
	}
	// Whitespace is a value: no trimming, no normalization, no refusal.
	if got, err := prepareBasicProbeScalars(basicProbeObservation{complete: true, videoCodec: " h264 ", duration: 100}); err != nil || got.videoCodec != " h264 " {
		t.Fatalf("whitespace video codec must be kept verbatim: got=%+v err=%v", got, err)
	}
}

func TestBasicProbePayloadRefusals(t *testing.T) {
	base := basicProbeObservation{complete: true, container: "mkv", videoCodec: "h264", duration: 100}
	tests := []struct {
		name   string
		mutate func(*basicProbeObservation)
		want   error
	}{
		{"incomplete probe", func(o *basicProbeObservation) { o.complete = false }, ErrInvalid},
		{"empty video codec", func(o *basicProbeObservation) { o.videoCodec = "" }, ErrInvalid},
		{"negative width", func(o *basicProbeObservation) { o.width = -1 }, ErrInvalid},
		{"negative height", func(o *basicProbeObservation) { o.height = -1 }, ErrInvalid},
		{"zero duration", func(o *basicProbeObservation) { o.duration = 0 }, ErrInvalid},
		{"negative duration", func(o *basicProbeObservation) { o.duration = -1 }, ErrInvalid},
		{"nan duration", func(o *basicProbeObservation) { o.duration = math.NaN() }, ErrInvalid},
		{"positive infinite duration", func(o *basicProbeObservation) { o.duration = math.Inf(1) }, ErrInvalid},
		{"negative infinite duration", func(o *basicProbeObservation) { o.duration = math.Inf(-1) }, ErrInvalid},
		{"invalid utf8 container", func(o *basicProbeObservation) { o.container = string([]byte{0xff}) }, ErrInvalid},
		{"invalid utf8 video codec", func(o *basicProbeObservation) { o.videoCodec = string([]byte{0xff}) }, ErrInvalid},
		{"invalid utf8 audio codec", func(o *basicProbeObservation) { o.audioCodec = string([]byte{0xff}) }, ErrInvalid},
		{"oversized container", func(o *basicProbeObservation) { o.container = strings.Repeat("c", basicProbePayloadMaxBytes+1) }, ErrCap},
		{"oversized video codec", func(o *basicProbeObservation) { o.videoCodec = strings.Repeat("v", basicProbePayloadMaxBytes+1) }, ErrCap},
		{"oversized audio codec", func(o *basicProbeObservation) { o.audioCodec = strings.Repeat("a", basicProbePayloadMaxBytes+1) }, ErrCap},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			observed := base
			tc.mutate(&observed)
			got, err := prepareBasicProbeScalars(observed)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got err=%v want %v", err, tc.want)
			}
			if got != (basicProbeScalars{}) {
				t.Fatalf("refusal must return the zero tuple: got=%+v", got)
			}
		})
	}
	// A field exactly at the cap is legal.
	atCap := base
	atCap.audioCodec = strings.Repeat("a", basicProbePayloadMaxBytes)
	if got, err := prepareBasicProbeScalars(atCap); err != nil || len(got.audioCodec) != basicProbePayloadMaxBytes {
		t.Fatalf("exactly-capped field must be admitted: got=%d err=%v", len(got.audioCodec), err)
	}
}

func TestBasicProbePayloadClonesStrings(t *testing.T) {
	// Back the observation with mutable buffers so aliasing is observable.
	container, videoCodec, audioCodec := []byte("mkv"), []byte("h264"), []byte("aac")
	got, err := prepareBasicProbeScalars(basicProbeObservation{
		complete:   true,
		container:  unsafe.String(&container[0], len(container)),
		videoCodec: unsafe.String(&videoCodec[0], len(videoCodec)),
		audioCodec: unsafe.String(&audioCodec[0], len(audioCodec)),
		duration:   100,
	})
	if err != nil {
		t.Fatal(err)
	}
	container[0], videoCodec[0], audioCodec[0] = 'X', 'Y', 'Z'
	if got.container != "mkv" || got.videoCodec != "h264" || got.audioCodec != "aac" {
		t.Fatalf("result aliases caller buffers: %+v", got)
	}
}
