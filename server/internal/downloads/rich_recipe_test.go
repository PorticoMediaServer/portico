package downloads

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

// The HEVC recipe is chosen only when it keeps something, and only for a
// device whose own planner plays its result.
func TestRichRecipeKeepsHDRAndSurroundForDevicesThatPlayThem(t *testing.T) {
	hdr := playback.DeliverySource{Container: "mkv", Video: &playback.SourceVideo{Codec: "hevc", StreamDetail: assets.StreamDetail{BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 23.976, DynamicRange: assets.RangeHDR10}}, Audio: []playback.SourceAudio{{Codec: "truehd", Channels: 8}}}
	sdr := playback.DeliverySource{Container: "mkv", Video: &playback.SourceVideo{Codec: "h264", StreamDetail: assets.StreamDetail{BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: assets.RangeSDR}}, Audio: []playback.SourceAudio{{Codec: "aac", Channels: 2}}}
	if !richGains(hdr) || richGains(sdr) {
		t.Fatal("gains")
	}
	recipe, err := preparedmedia.ProfileByID(preparedmedia.RichProfileFor("1080p-low"))
	if err != nil {
		t.Fatal(err)
	}
	target := richTarget(hdr, recipe)
	v := target.Video
	if v.Codec != "hevc" || v.BitDepth != 10 || v.DynamicRange != assets.RangeHDR10 || v.Width != 1920 || v.Height != 1080 || target.Audio[0].Channels != 8 || target.Audio[0].Codec != "flac" {
		t.Fatalf("%+v %+v", v, target.Audio)
	}
	// A 5.1 source keeps AAC; only a 7.1 source needs E-AC-3.
	fiveOne := hdr
	fiveOne.Audio = []playback.SourceAudio{{Codec: "eac3", Channels: 6}}
	if fiveOneTarget := richTarget(fiveOne, recipe); fiveOneTarget.Audio[0].Channels != 6 || fiveOneTarget.Audio[0].Codec != "aac" {
		t.Fatalf("5.1 was not kept as AAC: %+v", fiveOneTarget.Audio)
	}
	// A device that knows only H.264 and stereo is not given HEVC.
	if playback.DownloadPlayable(target, playback.BaselineCapabilityDocument()) {
		t.Fatal("the baseline device was offered an HEVC copy")
	}
	device := playback.BaselineCapabilityDocument()
	device.Client.Family = "apple"
	device.Display.DynamicRanges = []string{"sdr", "hdr10"}
	device.AudioOutput.MaxChannels = 6
	device.Engines.Native.Evidence = playback.EvidenceDeclared
	device.Engines.Native.Video = append(device.Engines.Native.Video, playback.ClientVideoCodec{Codec: "hevc", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr", "hdr10", "hlg"}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60})
	device.Engines.Native.Audio = []playback.ClientAudioCodec{{Codec: "aac", MaxChannels: 6}, {Codec: "flac", MaxChannels: 8}}
	device.Engines.Native.Transports = []playback.ClientTransport{{Transport: playback.TransportDirect, Containers: []string{"mp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "flac"}}}
	if err := device.Accept(); err != nil {
		t.Fatal(err)
	}
	if !playback.DownloadPlayable(target, device) {
		t.Fatal("an HEVC HDR10 device was not offered the HEVC copy")
	}
	// Dolby Vision keeps its HDR10 base layer; profile 5 has none to keep.
	dv := *hdr.Video
	dv.DynamicRange, dv.DolbyVisionProfile, dv.DolbyVisionCompatibility = assets.RangeDolbyVision, 8, 1
	if richRange(&dv) != assets.RangeHDR10 {
		t.Fatal("profile 8.1 base")
	}
	dv.DolbyVisionProfile, dv.DolbyVisionCompatibility = 5, 0
	if richRange(&dv) != assets.RangeSDR {
		t.Fatal("profile 5 has no base to keep")
	}
}
