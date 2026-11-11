package streaming

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// What the planner gives every declared device for seven sources, at Original and at a
// 720p limit, on four kinds of server (no chip; a chip that encodes H.264; one that also
// encodes HEVC 10-bit; one that also encodes AV1 10-bit): the route, the output's codec,
// bit depth and range, and whether the chip makes it. The table is
// testdata/conversion_choices.txt; any change to a choice shows here as the lines that
// differ. PORTICO_UPDATE_CHOICES=1 writes the table again after a change that is meant.
func TestConversionChoicesForEveryDevice(t *testing.T) {
	facts := &decoder.ToolchainFacts{Identity: "unit-selected-device",
		Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true},
		Decoders: map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true, "vc1": true, "truehd": true, "aac": true},
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
	if backend == "" {
		t.Skip("no compiled hardware backend on this platform")
	}
	aac := SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}}
	sources := map[string]*DeliverySource{
		"vc1-8bit-sdr-1080":        videoSource("mkv", "vc1", mediafacts.StreamDetail{Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 8, DynamicRange: mediafacts.RangeSDR}, aac),
		"h264-hi10-10bit-sdr-1080": videoSource("mkv", "h264", mediafacts.StreamDetail{Profile: "High 10", Level: 41, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeSDR}, aac),
		"hevc-10bit-sdr-2160":      videoSource("mkv", "hevc", mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeSDR, CodecTag: "hvc1"}, aac),
		"hevc-hdr10-2160":          videoSource("mkv", "hevc", mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10, CodecTag: "hvc1"}, aac),
		"hevc-hlg-2160":            videoSource("mkv", "hevc", mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHLG, CodecTag: "hvc1"}, aac),
		"vp9-hdr10-2160":           videoSource("mkv", "vp9", mediafacts.StreamDetail{Profile: "Profile 2", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10}, aac),
		"av1-hdr10-2160":           videoSource("mkv", "av1", mediafacts.StreamDetail{Profile: "Main", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: mediafacts.RangeHDR10}, aac),
	}
	targets := map[string]QualityTarget{
		"original": {RungID: "original", Kind: QualityAutomatic, AllowHDR: true},
		"720p":     {RungID: "720p-high", MaxVideoHeight: 720, MaxVideoBitrateBPS: 3_000_000, AllowHDR: true},
	}
	chips := map[string][]string{"no-chip": nil, "chip-h264": {"h264:8"}, "chip-h264-hevc10": {"h264:8", "hevc:8", "hevc:10"},
		"chip-h264-hevc10-av1": {"h264:8", "hevc:8", "hevc:10", "av1:8", "av1:10"}}
	var lines []string
	for name, doc := range plannerDocuments(t) {
		for sourceName, source := range sources {
			for targetName, target := range targets {
				for chipName, encodes := range chips {
					selected := decoder.HardwareProbe{}
					if encodes != nil {
						selected = chip(encodes...)
						selected.Backend = backend
					}
					doc := doc
					p, err := planDelivery(DeliveryInput{Source: source, Device: &doc, Server: facts, Hardware: selected, Policy: openPolicy(),
						Target: target, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
					got := ""
					if err != nil {
						got = "refused: " + err.Error()
					} else {
						where := "software"
						if p.HardwareBackend != "" && p.HardwareBackend != string(decoder.BackendSoftware) {
							where = "chip"
						}
						got = fmt.Sprintf("%s %s %d %s %s", p.Mode, p.OutputVideoCodec, p.OutputBitDepth, p.Trace.Video.OutputRange, where)
						if p.OutputVideoCodec == "" {
							got = fmt.Sprintf("%s (no video conversion)", p.Mode)
						}
					}
					lines = append(lines, fmt.Sprintf("%-14s | %-25s | %-8s | %-20s => %s", name, sourceName, targetName, chipName, got))
				}
			}
		}
	}
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"
	path := filepath.Join("testdata", "conversion_choices.txt")
	if os.Getenv("PORTICO_UPDATE_CHOICES") == "1" {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		want[line] = true
	}
	changed := 0
	for _, line := range lines {
		if !want[line] {
			changed++
			if changed <= 40 {
				t.Errorf("now: %s", line)
			}
		}
	}
	if changed > 0 || len(lines) != len(want) {
		t.Fatalf("%d of %d choices differ from %s (%d lines there); set PORTICO_UPDATE_CHOICES=1 if the change is meant", changed, len(lines), path, len(want))
	}
}
