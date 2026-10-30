package playback

import (
	"encoding/json"
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"testing"
)

// Device declarations are shipped client fixtures, with only explicitly
// named variants changed (older hardware, no HEVC hardware, or player setting).
func plannerDocuments(t *testing.T) map[string]CapabilityDocument {
	t.Helper()
	raw, err := os.ReadFile("testdata/client-profiles.json")
	if err != nil {
		t.Fatal(err)
	}
	var documents map[string]CapabilityDocument
	if err := json.Unmarshal(raw, &documents); err != nil {
		t.Fatal(err)
	}
	old := documents["tvos"]
	old.Engines.Native.Video = append([]ClientVideoCodec{}, old.Engines.Native.Video[:1]...)
	old.Engines.Native.Video[0].MaxWidth, old.Engines.Native.Video[0].MaxHeight = 1920, 1080
	old.Display = ClientDisplay{Width: 1920, Height: 1080, DynamicRanges: []string{"sdr"}}
	noHEVC := documents["android"]
	copyNative := *noHEVC.Engines.Native
	noHEVC.Engines.Native = &copyNative
	noHEVC.Engines.Native.Video = nil
	for _, v := range documents["android"].Engines.Native.Video {
		if v.Codec != "hevc" {
			noHEVC.Engines.Native.Video = append(noHEVC.Engines.Native.Video, v)
		}
	}
	mpv := documents["appletv_mpv"]
	mpv.EngineSetting = EngineSettingMPV
	firefox := documents["chromium"]
	firefox.Client.Platform = "firefox"
	out := map[string]CapabilityDocument{"AppleTV4K-native": documents["appletv_dv"], "AppleTV4K-mpv": mpv, "AppleTVHD": old, "iPhone": documents["ios"], "Android-HEVC": documents["android"], "Android-no-HEVC": noHEVC, "FireTVStick": documents["firetv"], "Chromecast": documents["chromecast"], "Chrome": documents["chromium"], "Safari": documents["safari_hdr"], "Firefox": firefox, "Vega": documents["vega"]}
	for name, d := range out {
		if err := d.Accept(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = d
	}
	return out
}

func plannerSources() []*DeliverySource {
	sdr := assets.StreamDetail{Profile: "High", Level: 40, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr", CodecTag: "avc1"}
	hdr := assets.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: "hdr10", CodecTag: "hvc1"}
	audio := SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: assets.StreamDetail{SampleRate: 48000, BitRate: 192000}}
	truehd := SourceAudio{Index: 1, Codec: "truehd", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{SampleRate: 96000, BitDepth: 24, BitRate: 4000000}}
	sources := []*DeliverySource{videoSource("mp4", "h264", sdr, audio), videoSource("mkv", "hevc", hdr, truehd)}
	for _, profile := range []int{5, 7, 8} {
		dv := hdr
		dv.DynamicRange = "dolby_vision"
		dv.DolbyVisionProfile = profile
		dv.CodecTag = "dvh1"
		if profile == 8 {
			dv.DolbyVisionCompatibility = 1
		}
		sources = append(sources, videoSource("mp4", "hevc", dv, audio))
	}
	av1 := sdr
	av1.Profile = "Main"
	av1.Level = 0
	av1.CodecTag = "av01"
	mpeg := sdr
	mpeg.Profile = "Main"
	mpeg.Level = 0
	mpeg.Interlaced = true
	mpeg.CodecTag = ""
	vc1 := sdr
	vc1.Profile = "Advanced"
	vc1.Level = 0
	vc1.CodecTag = ""
	hlg := hdr
	hlg.DynamicRange = "hlg"
	sources = append(sources, videoSource("mp4", "av1", av1, audio), videoSource("mkv", "mpeg2video", mpeg, SourceAudio{Index: 1, Codec: "ac3", Channels: 6, StreamDetail: assets.StreamDetail{SampleRate: 48000, BitRate: 640000}}), videoSource("mkv", "vc1", vc1, SourceAudio{Index: 1, Codec: "dts", Channels: 8, StreamDetail: assets.StreamDetail{Profile: "DTS-HD MA", SampleRate: 96000, BitDepth: 24}}), videoSource("mp4", "hevc", hlg, audio))
	return sources
}

// Compact expected plans name the route, video/audio codecs, channel count,
// range and engine. Rejected no-base DV has no plausible fallback picture.
func TestPlannerOutputDeviceSourceMatrix(t *testing.T) {
	names := []string{"H264-SDR", "HEVC-HDR10-TrueHD71", "DV5", "DV7", "DV8", "AV1", "MPEG2-interlaced-AC3", "VC1-DTSHD", "HLG"}
	facts := &decoder.ToolchainFacts{Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true}, Decoders: map[string]bool{"h264": true, "hevc": true, "av1": true, "mpeg2video": true, "vc1": true, "truehd": true, "aac": true, "ac3": true, "dca": true}, Filters: map[string]bool{"zscale": true, "tonemap": true, "bwdif": true}, BitstreamFilters: map[string]bool{"dovi_rpu": true, "filter_units": true}}
	expected := map[string][]string{
		"Vega":             {"original/h264/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "refused", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native"},
		"AppleTV4K-mpv":    {"original/h264/aac/2/sdr/mpv", "original/hevc/truehd/8/hdr10/mpv", "original/hevc/aac/2/dolby_vision/native", "copy_remux/hevc/aac/2/hdr10/mpv", "copy_remux/hevc/aac/2/hdr10/mpv", "original/av1/aac/2/sdr/mpv", "original/mpeg2video/ac3/6/sdr/mpv", "original/vc1/dts/8/sdr/mpv", "original/hevc/aac/2/hlg/mpv"},
		"iPhone":           {"original/h264/aac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "refused", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/ac3/6/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native"},
		"Android-HEVC":     {"original/h264/aac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "refused", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native"},
		"Chrome":           {"original/h264/aac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "refused", "video_conversion/av1/aac/2/sdr/native", "video_conversion/av1/aac/2/sdr/native", "original/av1/aac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "video_conversion/av1/aac/2/sdr/native"},
		"Safari":           {"original/h264/aac/2/sdr/native", "audio_conversion/hevc/flac/6/hdr10/native", "refused", "copy_remux/hevc/aac/2/hdr10/native", "copy_remux/hevc/aac/2/hdr10/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/ac3/6/sdr/native", "video_conversion/hevc/flac/6/sdr/native", "original/hevc/aac/2/hlg/native"},
		"AppleTV4K-native": {"original/h264/aac/2/sdr/native", "audio_conversion/hevc/flac/8/hdr10/native", "original/hevc/aac/2/dolby_vision/native", "copy_remux/hevc/aac/2/hdr10/native", "original/hevc/aac/2/dolby_vision/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/ac3/6/sdr/native", "video_conversion/hevc/flac/8/sdr/native", "original/hevc/aac/2/hlg/native"},
		"AppleTVHD":        {"original/h264/aac/2/sdr/native", "video_conversion/h264/flac/2/sdr/native", "refused", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/ac3/6/sdr/native", "video_conversion/h264/flac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native"},
		"Android-no-HEVC":  {"original/h264/aac/2/sdr/native", "video_conversion/h264/flac/2/sdr/native", "refused", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/flac/2/sdr/native", "video_conversion/h264/flac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native"},
		"FireTVStick":      {"original/h264/aac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "refused", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "video_conversion/hevc/flac/2/sdr/native", "video_conversion/hevc/aac/2/sdr/native"},
		"Chromecast":       {"original/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "refused", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native", "video_conversion/h264/aac/2/sdr/native"},
		"Firefox":          {"original/h264/aac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "refused", "video_conversion/av1/aac/2/sdr/native", "video_conversion/av1/aac/2/sdr/native", "original/av1/aac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "video_conversion/av1/flac/2/sdr/native", "video_conversion/av1/aac/2/sdr/native"},
	}

	for name, doc := range plannerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			for i, source := range plannerSources() {
				t.Run(names[i], func(t *testing.T) {
					policy := openPolicy()
					p, err := planDelivery(DeliveryInput{Source: source, Device: &doc, Server: facts, Policy: policy, Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
					actual := "refused"
					if err == nil {
						channels := p.AudioChannels
						if channels == 0 {
							channels = source.Audio[0].Channels
						}
						dr := p.Trace.Video.OutputRange
						actual = fmt.Sprintf("%s/%s/%s/%d/%s/%s", p.Strategy, p.OutputVideoCodec, p.OutputAudioCodec, channels, dr, p.Engine)
						if p.VideoAction == "convert" || p.AudioAction == "convert" {
							if p.ConversionTarget == nil {
								t.Fatal("no executable target")
							}
							if _, _, _, err := p.CodecArgs(); err != nil {
								t.Fatal(err)
							}
						}
					}
					if actual != expected[name][i] {
						t.Fatalf("got %s, want %s (%v)", actual, expected[name][i], err)
					}
				})
			}
		})
	}
}
