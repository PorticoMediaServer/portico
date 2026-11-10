package streaming

import (
	"runtime"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// Hardware decoding off removes the proven decode formats from the probe a
// plan is made with, so the conversion graph decodes on the processor.
func TestHardwareDecodeOffKeepsDecodingOnTheProcessor(t *testing.T) {
	backend := decoder.BackendVAAPI
	switch runtime.GOOS {
	case "darwin":
		backend = decoder.BackendVideoToolbox
	case "windows":
		backend = decoder.BackendNVENC
	}
	probe := decoder.HardwareProbe{Backend: backend, Available: true, DecodeCodecs: map[string]bool{"hevc:10": true}}
	cfg := DefaultDeliveryConfiguration()
	if got := withoutHardwareDecode(probe, cfg); !got.DecodeCodecs["hevc:10"] {
		t.Fatal("decoding stays on the adapter by default")
	}
	cfg.SoftwareDecode = true
	got := withoutHardwareDecode(probe, cfg)
	if len(got.DecodeCodecs) != 0 || got.Backend != backend || !got.Available {
		t.Fatalf("off keeps encoding on the adapter and decoding on the processor: %+v", got)
	}
	decodes := func(p decoder.HardwareProbe) bool {
		graph, err := decoder.BuildConversion(decoder.ConversionRequest{Probe: p, ConvertVideo: true, SourceVideoCodec: "hevc", SourceBitDepth: 10,
			Target: &decoder.ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", Width: 1920, Height: 1080, VideoLevel: 41, AudioCodec: "aac", AudioChannels: 2}})
		if err != nil {
			t.Fatal(err)
		}
		for _, arg := range graph.Input {
			if arg == "-hwaccel" {
				return true
			}
		}
		return false
	}
	if !decodes(probe) {
		t.Fatal("the fixture must decode on the adapter when decoding is allowed")
	}
	if decodes(got) {
		t.Fatal("hardware decode arguments with decoding off")
	}
}
