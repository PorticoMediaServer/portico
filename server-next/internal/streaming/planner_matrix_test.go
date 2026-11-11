package streaming

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// Device declarations are shipped client fixtures, with only explicitly
// named variants changed (older hardware, no HEVC hardware, or player setting).
func plannerDocuments(t *testing.T) map[string]CapabilityDocument {
	t.Helper()
	documents := map[string]CapabilityDocument{}
	for _, name := range profileNames(t) {
		var d CapabilityDocument
		if err := json.Unmarshal(profileDocument(t, name), &d); err != nil {
			t.Fatal(err)
		}
		documents[name] = d
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
	// An iPhone that shows HDR (iPhone 12 and later).
	hdrPhone := documents["ios"]
	hdrPhone.Display.DynamicRanges = []string{"sdr", "hdr10", "hlg"}
	phoneNative := *hdrPhone.Engines.Native
	hdrPhone.Engines.Native = &phoneNative
	phoneNative.Video = append([]ClientVideoCodec{}, phoneNative.Video...)
	for i := range phoneNative.Video {
		if phoneNative.Video[i].Codec == "hevc" {
			phoneNative.Video[i].DynamicRanges = []string{"sdr", "hdr10", "hlg"}
		}
	}
	firefox := documents["chromium"]
	firefox.Client.Platform = "firefox"
	out := map[string]CapabilityDocument{
		"AppleTV4K-native": documents["appletv_dv"],
		"AppleTV4K-mpv":    mpv,
		"AppleTVHD":        old,
		"iPhone":           documents["ios"],
		"iPhone-HDR":       hdrPhone,
		"Android-HEVC":     documents["android"],
		"Android-no-HEVC":  noHEVC,
		"FireTVStick":      documents["firetv"],
		"Chromecast":       documents["chromecast"],
		"Chrome":           documents["chromium"],
		"Safari":           documents["safari_hdr"],
		"Firefox":          firefox,
		"Vega":             documents["vega"],
	}
	for name, d := range out {
		if err := d.Accept(); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = d
	}
	return out
}

func plannerSources() []*DeliverySource {
	sdr := mediafacts.StreamDetail{Profile: "High", Level: 40, Width: 1920, Height: 1080, FrameRate: 24, BitDepth: 8, DynamicRange: "sdr", CodecTag: "avc1"}
	hdr := mediafacts.StreamDetail{Profile: "Main 10", Level: 153, Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: "hdr10", CodecTag: "hvc1"}
	audio := SourceAudio{Index: 1, Codec: "aac", Channels: 2, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 192000}}
	truehd := SourceAudio{Index: 1, Codec: "truehd", Channels: 8, Layout: "7.1", StreamDetail: mediafacts.StreamDetail{SampleRate: 96000, BitDepth: 24, BitRate: 4000000}}
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
	// A 10-bit standard-range source, common for animation: a conversion keeps its 10 bits
	// where the device plays them and the server converts them in real time.
	deep := sdr
	deep.Profile = "High 10"
	deep.Level = 51
	deep.BitDepth = 10
	sources = append(
		sources,
		videoSource("mp4", "av1", av1, audio),
		videoSource("mkv", "mpeg2video", mpeg, SourceAudio{Index: 1, Codec: "ac3", Channels: 6, StreamDetail: mediafacts.StreamDetail{SampleRate: 48000, BitRate: 640000}}),
		videoSource("mkv", "vc1", vc1, SourceAudio{Index: 1, Codec: "dts", Channels: 8, StreamDetail: mediafacts.StreamDetail{Profile: "DTS-HD MA", SampleRate: 96000, BitDepth: 24}}),
		videoSource("mp4", "hevc", hlg, audio),
		videoSource("mkv", "h264", deep, audio),
		// An HDR film most TVs and phones cannot play as it is: a conversion keeps its HDR
		// only on a chip that encodes 10 bits, and this server has none.
		videoSource("mkv", "av1", mediafacts.StreamDetail{Profile: "Main", Width: 3840, Height: 2160, FrameRate: 24, BitDepth: 10, DynamicRange: "hdr10", CodecTag: "av01"}, audio),
	)
	return sources
}

// Compact expected plans name the route, video/audio codecs, channel count,
// range and engine. Rejected no-base DV has no plausible fallback picture.
func TestPlannerOutputDeviceSourceMatrix(t *testing.T) {
	names := []string{"H264-SDR", "HEVC-HDR10-TrueHD71", "DV5", "DV7", "DV8", "AV1", "MPEG2-interlaced-AC3", "VC1-DTSHD", "HLG", "H264-10bit-SDR", "AV1-HDR10"}
	facts := &decoder.ToolchainFacts{
		Encoders:         map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true},
		Decoders:         map[string]bool{"h264": true, "hevc": true, "av1": true, "mpeg2video": true, "vc1": true, "truehd": true, "aac": true, "ac3": true, "dca": true},
		Filters:          map[string]bool{"zscale": true, "tonemap": true, "bwdif": true},
		BitstreamFilters: map[string]bool{"dovi_rpu": true, "filter_units": true},
	}
	expected := map[string][]string{
		"iPhone-HDR": {
			"original/h264/aac/2/sdr/native",
			"audio_conversion/hevc/flac/2/hdr10/native",
			"refused",
			"copy_remux/hevc/aac/2/hdr10/native",
			"copy_remux/hevc/aac/2/hdr10/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/ac3/6/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"original/hevc/aac/2/hlg/native",
			"video_conversion/hevc/aac/2/sdr/native/10bit",
			"video_conversion/hevc/aac/2/sdr/native",
		},
		"Vega": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"refused",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native/10bit",
			"video_conversion/hevc/aac/2/sdr/native",
		},
		"AppleTV4K-mpv": {
			"original/h264/aac/2/sdr/mpv",
			"original/hevc/truehd/8/hdr10/mpv",
			"original/hevc/aac/2/dolby_vision/native",
			"copy_remux/hevc/aac/2/hdr10/mpv",
			"copy_remux/hevc/aac/2/hdr10/mpv",
			"original/av1/aac/2/sdr/mpv",
			"original/mpeg2video/ac3/6/sdr/mpv",
			"original/vc1/dts/8/sdr/mpv",
			"original/hevc/aac/2/hlg/mpv",
			"original/h264/aac/2/sdr/mpv",
			// main kept HDR10 here on software HEVC; with no chip that encodes 10 bits, SDR.
			"video_conversion/hevc/aac/2/sdr/mpv",
		},
		"iPhone": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"refused",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/ac3/6/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native/10bit",
			"video_conversion/hevc/aac/2/sdr/native",
		},
		"Android-HEVC": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"refused",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native/8bit",
			"video_conversion/hevc/aac/2/sdr/native",
		},
		// An HDR film tone-mapped for a browser is software H.264, not software
		// AV1 10-bit, which could not keep up with a 4K film.
		"Chrome": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"refused",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"original/av1/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			// main gave software AV1 10-bit here: not real time, so H.264 8-bit.
			"video_conversion/h264/aac/2/sdr/native/8bit",
			"video_conversion/h264/aac/2/sdr/native",
		},
		"Safari": {
			"original/h264/aac/2/sdr/native",
			"audio_conversion/hevc/flac/6/hdr10/native",
			"refused",
			"copy_remux/hevc/aac/2/hdr10/native",
			"copy_remux/hevc/aac/2/hdr10/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/ac3/6/sdr/native",
			"video_conversion/hevc/flac/6/sdr/native",
			"original/hevc/aac/2/hlg/native",
			"video_conversion/hevc/aac/2/sdr/native/10bit",
			// main kept HDR10 here on software HEVC; with no chip that encodes 10 bits, SDR.
			"video_conversion/h264/aac/2/sdr/native",
		},
		"AppleTV4K-native": {
			"original/h264/aac/2/sdr/native",
			"audio_conversion/hevc/flac/8/hdr10/native",
			"original/hevc/aac/2/dolby_vision/native",
			"copy_remux/hevc/aac/2/hdr10/native",
			"original/hevc/aac/2/dolby_vision/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/ac3/6/sdr/native",
			"video_conversion/hevc/flac/8/sdr/native",
			"original/hevc/aac/2/hlg/native",
			"video_conversion/hevc/aac/2/sdr/native/10bit",
			// main kept HDR10 here on software HEVC; with no chip that encodes 10 bits, SDR.
			"video_conversion/hevc/aac/2/sdr/native",
		},
		"AppleTVHD": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"refused",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/ac3/6/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native/8bit",
			"video_conversion/h264/aac/2/sdr/native",
		},
		"Android-no-HEVC": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"refused",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native/8bit",
			"video_conversion/h264/aac/2/sdr/native",
		},
		"FireTVStick": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"refused",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"video_conversion/hevc/flac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native",
			"video_conversion/hevc/aac/2/sdr/native/8bit",
			"video_conversion/hevc/aac/2/sdr/native",
		},
		"Chromecast": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"refused",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native/8bit",
			"video_conversion/h264/aac/2/sdr/native",
		},
		"Firefox": {
			"original/h264/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"refused",
			"video_conversion/h264/aac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			"original/av1/aac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/flac/2/sdr/native",
			"video_conversion/h264/aac/2/sdr/native",
			// main gave software AV1 10-bit here: not real time, so H.264 8-bit.
			"video_conversion/h264/aac/2/sdr/native/8bit",
			"video_conversion/h264/aac/2/sdr/native",
		},
	}

	for name, doc := range plannerDocuments(t) {
		t.Run(name, func(t *testing.T) {
			for i, source := range plannerSources() {
				t.Run(names[i], func(t *testing.T) {
					policy := openPolicy()
					p, err := planDelivery(
						DeliveryInput{
							Source:             source,
							Device:             &doc,
							Server:             facts,
							Policy:             policy,
							Target:             QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true},
							Config:             DefaultDeliveryConfiguration(),
							TranscodingEnabled: true,
							FMP4Output:         true,
						},
					)
					actual := "refused"
					if err == nil {
						channels := p.AudioChannels
						if channels == 0 {
							channels = source.Audio[0].Channels
						}
						dr := p.Trace.Video.OutputRange
						actual = fmt.Sprintf("%s/%s/%s/%d/%s/%s", p.Strategy, p.OutputVideoCodec, p.OutputAudioCodec, channels, dr, p.Engine)
						if names[i] == "H264-10bit-SDR" && p.VideoAction == "convert" {
							// The bits a converted picture keeps are this row's point.
							actual += fmt.Sprintf("/%dbit", p.OutputBitDepth)
						}
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
