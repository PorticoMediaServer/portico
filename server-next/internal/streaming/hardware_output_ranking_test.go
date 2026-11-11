package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

func TestPlannerRanksOnlyMeasuredHardwareOutputsAheadOfSoftware(t *testing.T) {
	facts := plannerToolchain()
	probe := decoder.HardwareProbe{
		Available:      true,
		Identity:       "unit-selected-device",
		Device:         "unit-device",
		OutputEncoders: map[string]bool{"h264:8": true},
		OutputQuality:  map[string]bool{"h264:8": true},
	}
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
	source := videoSource("mkv", "vc1", mediafacts.StreamDetail{Width: 1280, Height: 720, BitDepth: 8, FrameRate: 24, DynamicRange: mediafacts.RangeSDR}, SourceAudio{Codec: "aac", Channels: 2})
	plan := func(selected decoder.HardwareProbe, format string) DeliveryPlan {
		t.Helper()
		cfg := DefaultDeliveryConfiguration()
		cfg.OutputFormat = format
		p, err := planDelivery(
			DeliveryInput{
				Source:             source,
				Client:             client,
				Server:             facts,
				Hardware:           selected,
				FMP4Output:         true,
				Policy:             openPolicy(),
				Target:             QualityTarget{RungID: "720p-high", MaxVideoHeight: 720, MaxVideoBitrateBPS: 3_000_000, AllowHDR: true},
				Config:             cfg,
				TranscodingEnabled: true,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	if p := plan(probe, "auto"); p.OutputVideoCodec != "h264" || p.HardwareDevice != probe.Device || p.HardwareBackend != string(probe.Backend) {
		t.Fatalf("measured hardware H264 was not chosen: %+v", p)
	}
	for _, unqualified := range []decoder.HardwareProbe{{}, {Backend: probe.Backend, Available: true, OutputEncoders: probe.OutputEncoders}, {Backend: probe.Backend, Available: true,
		Identity: probe.Identity}} {
		if p := plan(unqualified, "auto"); p.OutputVideoCodec != "h264" || p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative ||
			containsString(p.ReasonCodes, ReasonHardwareBackend) ||
			!containsString(p.ReasonCodes, ReasonSoftwareFallback) {
			t.Fatalf("unmeasured hardware changed codec ranking or execution explanation: %+v", p)
		}
	}
	mismatched := probe
	mismatched.Identity = "different-selected-toolchain"
	if p := plan(mismatched, "auto"); p.OutputVideoCodec != "h264" || p.HardwareBackend != string(decoder.BackendSoftware) || p.HardwareNative || containsString(p.ReasonCodes, ReasonHardwareBackend) {
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

// An HDR film for Chromium, with and without a chip that encodes H.264: Chromium is not
// shown HDR, so the picture is tone-mapped to standard range on H.264 (the chip's when
// there is one), not on software AV1 at 10 bits, which a 4K film would outrun.
func TestAnHDRFilmForChromiumIsToneMappedToH264(t *testing.T) {
	facts := &decoder.ToolchainFacts{Identity: "unit-selected-device",
		Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true},
		Decoders: map[string]bool{"h264": true, "hevc": true, "av1": true, "truehd": true, "aac": true},
		Filters:  map[string]bool{"zscale": true, "tonemap": true}}
	chip := decoder.HardwareProbe{Available: true, Identity: facts.Identity, Device: "unit-device",
		OutputEncoders: map[string]bool{"h264:8": true}, OutputQuality: map[string]bool{"h264:8": true}}
	for _, backend := range decoder.HardwareBackends {
		chip.Backend = backend
		if outputs := decoder.AvailableConversionOutputsForHardware(facts, chip); len(outputs.Video) > 0 && outputs.Video[0].Hardware {
			break
		}
		chip.Backend = ""
	}
	chromium := plannerDocuments(t)["Chrome"]
	hdr := mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10, CodecTag: "hvc1"}
	source := videoSource("mkv", "hevc", hdr, SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}})
	for _, selected := range []decoder.HardwareProbe{{}, chip} {
		if selected.Available && selected.Backend == "" {
			continue // no compiled hardware backend on this platform
		}
		p, err := planDelivery(DeliveryInput{Source: source, Device: &chromium, Server: facts, Hardware: selected, Policy: openPolicy(),
			Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
		if err != nil {
			t.Fatal(err)
		}
		if p.OutputVideoCodec != "h264" || p.OutputBitDepth != 8 || p.Trace.Video.OutputRange != mediafacts.RangeSDR || !p.ToneMap {
			t.Fatalf("chip %q: %s %d-bit %s, tone map %v", selected.Backend, p.OutputVideoCodec, p.OutputBitDepth, p.Trace.Video.OutputRange, p.ToneMap)
		}
		t.Logf("chip %q: %s %d-bit on %s", selected.Backend, p.OutputVideoCodec, p.OutputBitDepth, p.HardwareBackend)
		if selected.Backend != "" && p.HardwareBackend != string(chip.Backend) {
			t.Fatalf("the chip's H.264 was not used: %s", p.HardwareBackend)
		}
	}
}
