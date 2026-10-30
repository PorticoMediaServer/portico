package playback

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
)

func TestContainerFamiliesUseMeasuredNamesOnBothSides(t *testing.T) {
	for _, family := range [][]string{
		{"matroska", "mkv", "mka"},
		{"mpegts", "ts", "m2ts", "mts"},
		{"mov", "mp4", "m4v", "m4a", "m4b"},
		{"aac", "adts"},
	} {
		for _, declared := range family {
			for _, measured := range family {
				if !containersAllow([]string{declared}, measured) {
					t.Fatalf("%s rejected measured %s", declared, measured)
				}
			}
		}
	}
	for _, pair := range [][2]string{{"webm", "matroska"}, {"matroska", "webm"}, {"webm", "mkv"}, {"mp4", "mpegts"}, {"", ""}} {
		if containersAllow([]string{pair[0]}, pair[1]) {
			t.Fatal("unrelated or unobserved format admitted", pair)
		}
	}
}

// These facts come from real FFprobe output, including its comma-separated
// demuxer aliases. Neither the measured container nor the stream facts are
// replaced by extension-shaped planner fixtures.
func TestDirectPlansFromRealProbedContainerNames(t *testing.T) {
	ffmpeg, ffprobe := decodertest.QualifiedFFmpeg(t), decodertest.QualifiedFFprobe(t)
	for _, tc := range []struct{ muxer, codec, declared, measured string }{
		{"matroska", "libx264", "mkv", "matroska"},
		{"mpegts", "libx264", "m2ts", "mpegts"},
		{"mp4", "libx264", "m4v", "mov"},
		{"webm", "libvpx-vp9", "webm", "webm"},
		{"matroska", "libvpx-vp9", "mkv", "matroska"},
	} {
		t.Run(tc.muxer+"/"+tc.codec, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "misleading.webm")
			out, err := exec.Command(ffmpeg, "-v", "error", "-nostdin", "-f", "lavfi", "-i", "testsrc2=size=160x90:rate=25", "-t", "0.2", "-an", "-c:v", tc.codec, "-threads", "1", "-pix_fmt", "yuv420p", "-f", tc.muxer, path).CombinedOutput()
			if err != nil {
				t.Fatalf("fixture encode: %v %s", err, out)
			}
			facts, err := (assets.Probe{Binary: ffprobe}).Inspect(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			if facts.Container != tc.measured {
				t.Fatalf("measured container = %q, want %q", facts.Container, tc.measured)
			}
			source := discSourceFacts("source", facts)
			client := mpvProfile(appleTVProfile())
			client.Transports = []ClientTransport{{Transport: TransportDirect, Containers: []string{tc.declared}, Video: []string{source.Video.Codec}}}
			in := DeliveryInput{Source: &source, Client: client, NoHLS: true, Policy: openPolicy(), Target: QualityTarget{AllowHDR: true}, Config: DefaultDeliveryConfiguration()}
			plan, err := planDelivery(in)
			if err != nil || plan.Strategy != DeliveryOriginal {
				t.Fatal("measured source did not direct-play with HLS unavailable", plan, err)
			}
			if tc.muxer == "matroska" {
				in.Client.Transports[0].Containers = []string{"webm"}
				if plan, err = planDelivery(in); err == nil {
					t.Fatal("generic Matroska admitted as WebM", plan)
				}
			}
		})
	}
}
