package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

func TestPlannerRanksOnlyMeasuredHardwareOutputsAheadOfSoftware(t *testing.T) {
	facts := plannerToolchain()
	probe := decoder.HardwareProbe{Available: true, Identity: "unit-selected-device", Device: "unit-device", OutputEncoders: map[string]bool{"h264:8": true}, OutputQuality: map[string]bool{"h264:8": true}}
	facts.Identity = probe.Identity
	found := false
	for _, backend := range decoder.HardwareBackends {
		probe.Backend = backend
		for _, output := range decoder.AvailableConversionOutputsForHardware(facts, probe).Video {
			found = found || output.Hardware
		}
		if found {
			break
		}
	}
	if !found {
		t.Skip("no compiled hardware backend on this platform")
	}
	client := allOutputClient()
	client.Video = []ClientVideoCodec{client.Video[0], client.Video[len(client.Video)-1]}
	source := videoSource("mkv", "vc1", assets.StreamDetail{Width: 1280, Height: 720, BitDepth: 8, FrameRate: 24, DynamicRange: assets.RangeSDR}, SourceAudio{Codec: "aac", Channels: 2})
	plan := func(selected decoder.HardwareProbe, format string) DeliveryPlan {
		t.Helper()
		cfg := DefaultDeliveryConfiguration()
		cfg.OutputFormat = format
		p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: facts, Hardware: selected, FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "720p-high", MaxVideoHeight: 720, MaxVideoBitrateBPS: 3_000_000, AllowHDR: true}, Config: cfg, TranscodingEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := plan(probe, "auto"); p.OutputVideoCodec != "h264" || p.HardwareDevice != probe.Device || p.HardwareBackend != string(probe.Backend) {
		t.Fatalf("measured hardware H264 lost to software AV1: %+v", p)
	}
	for _, unqualified := range []decoder.HardwareProbe{{}, {Backend: probe.Backend, Available: true, OutputEncoders: probe.OutputEncoders}, {Backend: probe.Backend, Available: true, Identity: probe.Identity}} {
		if p := plan(unqualified, "auto"); p.OutputVideoCodec != "av1" || p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative || containsString(p.ReasonCodes, ReasonHardwareBackend) || !containsString(p.ReasonCodes, ReasonSoftwareFallback) {
			t.Fatalf("unmeasured hardware changed codec ranking or execution explanation: %+v", p)
		}
	}
	mismatched := probe
	mismatched.Identity = "different-selected-toolchain"
	if p := plan(mismatched, "auto"); p.OutputVideoCodec != "av1" || p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative || containsString(p.ReasonCodes, ReasonHardwareBackend) {
		t.Fatalf("another toolchain's measured output changed ranking or execution explanation: %+v", p)
	}
	wrongCell := probe
	wrongCell.NativeFilters, wrongCell.ScaleFilters = true, true
	wrongCell.OutputEncoders = map[string]bool{"hevc:10": true, "h264:10": true}
	if p := plan(wrongCell, "h264"); p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative || containsString(p.ReasonCodes, ReasonHardwareBackend) || p.ConversionProbe != nil {
		t.Fatalf("unmeasured H2648 retained hardware execution: %+v", p)
	}
	unknownBundle := probe
	facts.Identity = ""
	if p := plan(unknownBundle, "h264"); p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative || p.ConversionProbe != nil {
		t.Fatalf("unknown toolchain inherited hardware: %+v", p)
	}
	facts.Identity = probe.Identity
	if p := plan(decoder.HardwareProbe{}, "h264"); p.OutputVideoCodec != "h264" {
		t.Fatal("owner H264-only conversion format lost", p)
	}
}
