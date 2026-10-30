package decoder

import (
	"context"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"testing"
	"time"
)

// The confined probe against a real ffmpeg with its real dependency set. This is
// the configuration production uses, and it is the only way to find out whether
// the confinement profile actually lets an encoder start.
func TestHardwareProbeConfinedWithResolvedLibraries(t *testing.T) {
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	ffprobe := decodertest.QualifiedFFprobe(t)
	if !CheckConfinement(context.Background()) {
		decodertest.Unavailable(t, "decoder confinement is unavailable on this host")
	}
	libraries, err := ResolveLibraries(ffmpeg, ffprobe, nil)
	if err != nil {
		t.Fatal("decoder dependencies could not be resolved: " + err.Error())
	}
	// The software row in Report is a fallback declaration, not an executed
	// probe. Require a real bounded encode through the same confined launcher
	// so a launch failure affecting every backend cannot pass on that row.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	facts, err := ProbeToolchain(ctx, ffmpeg)
	if err != nil {
		t.Fatal("probing the qualified toolchain", err)
	}
	if !facts.Encoders["libx264"] {
		decodertest.Unavailable(t, "the software probe requires libx264")
	}
	cmd, err := probeCommand(ctx, ffmpeg, []string{"-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=64x64:rate=1", "-frames:v", "1", "-c:v", "libx264", "-preset", "ultrafast", "-f", "null", "-"}, libraries, nil)
	if err != nil {
		t.Fatal("constructing the confined software encode", err)
	}
	cmd.WaitDelay = time.Second
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("confined software encode did not run successfully: %v: %s", err, out)
	}
	detector := &HardwareDetector{Libraries: libraries, Timeout: 30 * time.Second}
	report := detector.Report(context.Background(), ffmpeg, "")
	for _, probe := range report {
		t.Logf("%s available=%v reason=%s native=%v %dms", probe.Backend, probe.Available, probe.Reason, probe.NativeFilters, probe.DurationMS)
	}
	// Software always works; a host with no hardware is a valid outcome, but a
	// probe that claims availability must have run and must name its encoder.
	for _, probe := range report {
		if probe.Available && probe.Backend != BackendSoftware && probe.Encoder == "" {
			t.Fatal("available backend without an encoder", probe)
		}
	}
	configured := os.Getenv("PORTICO_TEST_HARDWARE_BACKEND")
	if configured == "" {
		configured = "auto"
	} else if !ValidBackend(configured) || configured == string(BackendSoftware) {
		t.Fatal("fixture must declare an actual hardware backend", configured)
	}
	selected := detector.Select(context.Background(), ffmpeg, configured, os.Getenv("PORTICO_TEST_HARDWARE_DEVICE"))
	if !selected.Available {
		t.Fatal("selection produced no usable backend", selected)
	}
	if configured != "auto" && (selected.Backend != HardwareBackend(configured) || selected.Encoder == "" || selected.Reason != ProbeAvailable) {
		t.Fatal("declared hardware fixture fell back instead of encoding", selected)
	}
}
