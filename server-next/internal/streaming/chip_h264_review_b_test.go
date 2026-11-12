package streaming

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// An 8-bit 4K film that has to be converted, for every declared device: a chip that encodes
// only H.264 must not make the picture smaller than software would. (The seven sources of
// TestAChipNeverLowersTheConversion have no 8-bit film above 1080p, so it cannot see this.)
func TestAChipThatOnlyMakesH264DoesNotShrinkAnEightBit4KFilm(t *testing.T) {
	facts := &decoder.ToolchainFacts{Identity: "unit-selected-device",
		Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true},
		Decoders: map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true, "vc1": true, "truehd": true, "aac": true},
		Filters:  map[string]bool{"zscale": true, "tonemap": true}}
	probe := decoder.HardwareProbe{Available: true, Identity: facts.Identity, Device: "unit-device",
		OutputEncoders: map[string]bool{"h264:8": true}, OutputQuality: map[string]bool{"h264:8": true}}
	found := false
	for _, b := range decoder.HardwareBackends {
		probe.Backend = b
		if outputs := decoder.AvailableConversionOutputsForHardware(facts, probe); len(outputs.Video) > 0 && outputs.Video[0].Hardware {
			found = true
			break
		}
	}
	if !found {
		t.Skip("no compiled hardware backend on this platform")
	}
	aac := SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}}
	uhd8 := mediafacts.StreamDetail{Profile: "Advanced", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 8, DynamicRange: mediafacts.RangeSDR}
	source := videoSource("mkv", "vc1", uhd8, aac)
	target := QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}
	picture := func(doc CapabilityDocument, hardware decoder.HardwareProbe) string {
		p, err := planDelivery(DeliveryInput{Source: source, Device: &doc, Server: facts, Hardware: hardware, Policy: openPolicy(),
			Target: target, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
		if err != nil {
			return "refused: " + err.Error()
		}
		if p.ConversionTarget == nil {
			return string(p.Mode) + " (no video conversion)"
		}
		w, h := convertedDimensions(p)
		return fmt.Sprintf("%s %d-bit %s %dx%d", p.OutputVideoCodec, p.OutputBitDepth, p.Trace.Video.OutputRange, w, h)
	}
	documents := plannerDocuments(t)
	// Every profile file, not only the thirteen the planner tests name, each with its own
	// player and with mpv where it carries one.
	for _, name := range profileNames(t) {
		var d CapabilityDocument
		if err := json.Unmarshal(profileDocument(t, name), &d); err != nil {
			t.Fatal(err)
		}
		documents["profile "+name] = d
		if d.Engines.MPV != nil {
			d.EngineSetting = EngineSettingMPV
			documents["profile "+name+" (mpv)"] = d
		}
	}
	for name, doc := range documents {
		without, with := picture(doc, decoder.HardwareProbe{}), picture(doc, probe)
		if sizeOf(without) != sizeOf(with) {
			t.Errorf("%s: with no chip %s; with a chip that encodes only H.264 %s", name, without, with)
		}
	}
}

func sizeOf(picture string) string {
	for i := len(picture) - 1; i >= 0; i-- {
		if picture[i] == ' ' {
			return picture[i+1:]
		}
	}
	return picture
}
