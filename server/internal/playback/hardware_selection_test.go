package playback

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// Settings › Streaming › Device: an adapter id converts on that adapter's path
// alone, "software" on the processor, and an id no adapter has any more on the
// processor too, never on a different adapter.
func TestHardwareSelectionNarrowsConversionToTheChosenAdapter(t *testing.T) {
	devices := []decoder.HardwareDevice{
		{ID: "drm-first", Path: "/dev/dri/renderD128", Backends: []decoder.HardwareBackend{decoder.BackendVAAPI, decoder.BackendQSV}},
		{ID: "drm-second", Path: "/dev/dri/renderD129", Backends: []decoder.HardwareBackend{decoder.BackendVAAPI}},
	}
	s := &Service{hardware: &decoder.HardwareDetector{Devices: func(context.Context) []decoder.HardwareDevice { return devices }}}
	ctx := context.Background()
	base := DefaultDeliveryConfiguration()
	unknown := base
	unknown.HardwareSelection = "drm-first"
	if got := (&Service{}).hardwareConfiguration(ctx, unknown); got.HardwareBackend != "software" {
		t.Fatalf("before any discovery an adapter choice converts on the processor: %+v", got)
	}
	s.HardwareDevices(ctx) // a discovery, as at startup; session starts never discover

	if got := s.hardwareConfiguration(ctx, base); got.HardwareBackend != "auto" || got.HardwareDevice != "" {
		t.Fatalf("auto keeps the configured backend and device: %+v", got)
	}
	choice := base
	choice.HardwareSelection = "software"
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != "software" || got.HardwareDevice != "" {
		t.Fatalf("software: %+v", got)
	}
	choice.HardwareSelection = "drm-second"
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != "auto" || got.HardwareDevice != "/dev/dri/renderD129" {
		t.Fatalf("an adapter id converts on that adapter: %+v", got)
	}
	choice.HardwareBackend = string(decoder.BackendQSV)
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != "auto" || got.HardwareDevice != "/dev/dri/renderD129" {
		t.Fatalf("a backend the chosen adapter lacks yields to its own: %+v", got)
	}
	choice.HardwareSelection = "drm-first"
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != string(decoder.BackendQSV) || got.HardwareDevice != "/dev/dri/renderD128" {
		t.Fatalf("a backend the adapter has is kept: %+v", got)
	}
	choice.HardwareSelection = "drm-removed"
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != "software" {
		t.Fatalf("a removed adapter converts on the processor: %+v", got)
	}
	// An adapter the inventory lists with no proven backend or no device path is never
	// widened into automatic selection on some other adapter.
	devices = append(devices, decoder.HardwareDevice{ID: "nvidia-unqualified", Path: ""})
	s.HardwareDevices(ctx)
	choice.HardwareSelection = "nvidia-unqualified"
	if got := s.hardwareConfiguration(ctx, choice); got.HardwareBackend != "software" || got.HardwareDevice != "" {
		t.Fatalf("an unusable adapter converts on the processor: %+v", got)
	}
}

// Hardware decoding off removes the proven decode formats from the probe a
// plan is made with, so the conversion graph decodes on the processor.
func TestHardwareDecodeOffKeepsDecodingOnTheProcessor(t *testing.T) {
	probe := decoder.HardwareProbe{Backend: decoder.BackendVAAPI, Available: true, DecodeCodecs: map[string]bool{"hevc:10": true}}
	cfg := DefaultDeliveryConfiguration()
	if got := withoutHardwareDecode(probe, cfg); !got.DecodeCodecs["hevc:10"] {
		t.Fatal("decoding stays on the adapter by default")
	}
	cfg.SoftwareDecode = true
	got := withoutHardwareDecode(probe, cfg)
	if len(got.DecodeCodecs) != 0 || got.Backend != decoder.BackendVAAPI || !got.Available {
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
