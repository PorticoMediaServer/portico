package streaming

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// A tone-mapped picture is 8-bit standard range, as Plex makes it (owner, 16 Nov 2026): a
// browser with a standard display that takes H.264 and 10-bit AV1 gets H.264 for an HDR10
// film, not a 10-bit AV1 carrying an 8-bit picture. A 10-bit standard-range source, which is
// not tone-mapped, still keeps its bit depth.
func TestATonemappedPictureIsChosenAsEightBit(t *testing.T) {
	browser := chromeProfile()
	for i := range browser.Video {
		if browser.Video[i].Codec == "h264" {
			browser.Video[i].BitDepths = []int{8, 10}
		}
	}
	for i := range browser.Transports {
		if browser.Transports[i].Transport == TransportHLSFMP4 {
			browser.Transports[i].Video = []string{"h264", "av1"}
		}
	}
	outputs := DefaultConversionOutputs()
	outputs.Video = []ConversionVideoOutput{
		{Codec: "av1", BitDepth: 10, Ranges: []string{mediafacts.RangeSDR}, Transports: []string{TransportHLSFMP4}},
		{Codec: "av1", BitDepth: 8, Ranges: []string{mediafacts.RangeSDR}, Transports: []string{TransportHLSFMP4}},
		{Codec: "h264", BitDepth: 8, Ranges: []string{mediafacts.RangeSDR}, Transports: []string{TransportHLSTS, TransportHLSFMP4}},
	}
	surround := SourceAudio{Codec: "eac3", Channels: 6}
	hdr10 := mediafacts.StreamDetail{Profile: "Main 10", BitDepth: 10, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: mediafacts.RangeHDR10, BitRate: 8_000_000}
	p := decide(t, videoSource("mp4", "hevc", hdr10, surround), browser, func(in *DeliveryInput) { in.Outputs, in.FMP4Output, in.Server = &outputs, true, plannerToolchain() })
	if !p.ToneMap || p.OutputVideoCodec != "h264" || p.OutputBitDepth != 8 || p.OutputRange != mediafacts.RangeSDR {
		t.Fatalf("an HDR10 film for a standard display: %s %d-bit %s, tone-mapped %v", p.OutputVideoCodec, p.OutputBitDepth, p.OutputRange, p.ToneMap)
	}
	deep := mediafacts.StreamDetail{Profile: "Main 10", BitDepth: 10, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: mediafacts.RangeSDR, BitRate: 8_000_000}
	p = decide(t, videoSource("mp4", "hevc", deep, surround), browser, func(in *DeliveryInput) { in.Outputs, in.FMP4Output, in.Server = &outputs, true, plannerToolchain() })
	if p.ToneMap || p.OutputVideoCodec != "av1" || p.OutputBitDepth != 10 {
		t.Fatalf("a 10-bit standard film lost its bit depth: %s %d-bit, tone-mapped %v", p.OutputVideoCodec, p.OutputBitDepth, p.ToneMap)
	}
}
