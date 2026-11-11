package streaming

import (
	"fmt"
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

// What a browser is given for an HDR film it cannot play as it is, by what the server's chip
// encodes. A film that plays beats HDR that stutters: HDR is kept only on the chip's 10-bit
// encoder, and otherwise the picture is standard-range H.264, not software AV1 or HEVC at 10
// bits, which a 4K film would outrun. The chip's AV1 is used only when no other chip output
// reaches the browser, until AV1 after a seek is settled (proof finding 1 on #2604).
func TestWhatABrowserIsGivenForAnHDRFilm(t *testing.T) {
	facts := &decoder.ToolchainFacts{Identity: "unit-selected-device",
		Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true},
		Decoders: map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true, "truehd": true, "aac": true},
		Filters:  map[string]bool{"zscale": true, "tonemap": true}}
	chip := func(encodes ...string) decoder.HardwareProbe {
		p := decoder.HardwareProbe{Available: true, Identity: facts.Identity, Device: "unit-device", OutputEncoders: map[string]bool{}, OutputQuality: map[string]bool{}}
		for _, e := range encodes {
			p.OutputEncoders[e], p.OutputQuality[e] = true, true
		}
		return p
	}
	backend := decoder.HardwareBackend("")
	for _, b := range decoder.HardwareBackends {
		probe := chip("h264:8")
		probe.Backend = b
		if outputs := decoder.AvailableConversionOutputsForHardware(facts, probe); len(outputs.Video) > 0 && outputs.Video[0].Hardware {
			backend = b
			break
		}
	}
	documents := plannerDocuments(t)
	chromiumSDR, safariHDR := documents["Chrome"], documents["Safari"]
	chromiumHDR := documents["Chrome"]
	chromiumHDR.Display.DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	native := *chromiumHDR.Engines.Native
	native.Video = append([]ClientVideoCodec{}, native.Video...)
	for i := range native.Video {
		if native.Video[i].Codec == "av1" {
			native.Video[i].DynamicRanges = []string{"sdr", "hdr10", "hlg"}
		}
	}
	chromiumHDR.Engines.Native = &native
	if err := chromiumHDR.Accept(); err != nil {
		t.Fatal(err)
	}
	hevcHDR := mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10, CodecTag: "hvc1"}
	vp9HDR := mediafacts.StreamDetail{Profile: "Profile 2", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10}
	aac := SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}}
	for _, tc := range []struct {
		name   string
		doc    CapabilityDocument
		source *DeliverySource
		chip   []string // what the chip encodes; nil is no chip
		want   string   // codec, depth, range, and "chip" or "software"
	}{
		{"Chromium shown SDR, no chip", chromiumSDR, videoSource("mkv", "hevc", hevcHDR, aac), nil, "h264 8 sdr software"},
		{"Chromium shown SDR, chip H.264", chromiumSDR, videoSource("mkv", "hevc", hevcHDR, aac), []string{"h264:8"}, "h264 8 sdr chip"},
		{"Chromium shown SDR, chip H.264 and AV1", chromiumSDR, videoSource("mkv", "hevc", hevcHDR, aac), []string{"h264:8", "av1:8", "av1:10"}, "h264 8 sdr chip"},
		{"Chromium shown HDR, no chip", chromiumHDR, videoSource("mkv", "hevc", hevcHDR, aac), nil, "h264 8 sdr software"},
		{"Chromium shown HDR, chip H.264 and AV1", chromiumHDR, videoSource("mkv", "hevc", hevcHDR, aac), []string{"h264:8", "av1:10"}, "h264 8 sdr chip"},
		{"Chromium shown HDR, chip AV1 only", chromiumHDR, videoSource("mkv", "hevc", hevcHDR, aac), []string{"av1:10"}, "av1 10 hdr10 chip"},
		{"Safari shown HDR, no chip", safariHDR, videoSource("mkv", "vp9", vp9HDR, aac), nil, "h264 8 sdr software"},
		{"Safari shown HDR, chip HEVC 10-bit", safariHDR, videoSource("mkv", "vp9", vp9HDR, aac), []string{"hevc:10", "h264:8"}, "hevc 10 hdr10 chip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := decoder.HardwareProbe{}
			if tc.chip != nil {
				if backend == "" {
					t.Skip("no compiled hardware backend on this platform")
				}
				selected = chip(tc.chip...)
				selected.Backend = backend
			}
			p, err := planDelivery(DeliveryInput{Source: tc.source, Device: &tc.doc, Server: facts, Hardware: selected, Policy: openPolicy(),
				Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
			if err != nil {
				t.Fatal(err)
			}
			where := "software"
			if p.HardwareBackend != "" && p.HardwareBackend != string(decoder.BackendSoftware) {
				where = "chip"
			}
			got := fmt.Sprintf("%s %d %s %s", p.OutputVideoCodec, p.OutputBitDepth, p.Trace.Video.OutputRange, where)
			t.Log(got)
			if got != tc.want {
				t.Fatalf("got %s, want %s", got, tc.want)
			}
		})
	}
}

// onTheChip marks the server's 10-bit outputs as made by its graphics chip, which a
// conversion that keeps HDR now needs. No chip is probed, so the arguments are still the
// software encoder's: the path these tests check, and the one a chip falls back to.
func onTheChip(o ConversionOutputs) *ConversionOutputs {
	o.Video = append([]ConversionVideoOutput{}, o.Video...)
	for i := range o.Video {
		if o.Video[i].BitDepth >= 10 {
			o.Video[i].Hardware = true
		}
	}
	return &o
}
