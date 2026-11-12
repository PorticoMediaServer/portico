//go:build linux

package decoder

import (
	"context"
	"testing"
)

// "What this server can do" lists what the probes found, in the order playback tries the
// backends, from the kept verdicts only: a backend not tried is absent, one of another system
// is never listed, and one whose device stopped working since says so.
func TestKnownListsTheKeptVerdictsOnlyInTheOrderTheyAreTried(t *testing.T) {
	d := &HardwareDetector{CachedRuntimeIdentity: func(context.Context, string) (string, bool) { return "ffmpeg-1", true }}
	if got := d.Known(context.Background(), "ffmpeg"); len(got) != 0 {
		t.Fatalf("before any probe: %+v", got)
	}
	d.cache = map[string]HardwareProbe{
		"ffmpeg-1/" + string(BackendVAAPI) + "/": {Backend: BackendVAAPI, Available: true, Encoder: "h264_vaapi"},
		"ffmpeg-1/" + string(BackendNVENC) + "/": {Backend: BackendNVENC, Reason: ProbeEncodeFailed},
		"ffmpeg-1/" + string(BackendAMF) + "/":   {Backend: BackendAMF, Reason: ProbeUnsupportedPlatform},
		"ffmpeg-0/" + string(BackendQSV) + "/":   {Backend: BackendQSV, Available: true},
	}
	d.cache["ffmpeg-1/"+string(BackendNVENC)+"/card-two"] = HardwareProbe{Backend: BackendNVENC, Device: "card-two", Available: true,
		DecodeCodecs: map[string]bool{"hevc:10": true}, OutputQuality: map[string]bool{"h264:8": true}}
	if cell := d.KnownDevice(context.Background(), "ffmpeg", "card-two"); len(cell) != 1 || !cell[0].Available || !cell[0].DecodeCodecs["hevc:10"] || !cell[0].OutputQuality["h264:8"] {
		t.Fatalf("a physical card inherited the default device's verdict: %+v", cell)
	}
	got := d.Known(context.Background(), "ffmpeg")
	if len(got) != 2 || got[0].Backend != BackendNVENC || got[1].Backend != BackendVAAPI || !got[1].Available {
		t.Fatalf("the kept verdicts: %+v", got)
	}
	for range 3 {
		RecordHardwareDeviceFailure(BackendVAAPI, "")
	}
	t.Cleanup(func() { clearDeviceCircuit(BackendVAAPI, "") })
	if got := d.Known(context.Background(), "ffmpeg"); got[1].Available || got[1].Reason != ProbeCircuitOpen {
		t.Fatalf("a device that stopped working: %+v", got[1])
	}
	cold := &HardwareDetector{CachedRuntimeIdentity: func(context.Context, string) (string, bool) { return "", false }, cache: d.cache}
	if got := cold.Known(context.Background(), "ffmpeg"); got != nil {
		t.Fatalf("an executable not yet identified: %+v", got)
	}
}
