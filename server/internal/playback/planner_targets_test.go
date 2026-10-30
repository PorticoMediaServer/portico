package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"strconv"
	"strings"
	"testing"
)

func plannerToolchain() *decoder.ToolchainFacts {
	return &decoder.ToolchainFacts{Encoders: map[string]bool{"libx264": true, "libx265": true, "libaom-av1": true, "aac": true, "ac3": true, "eac3": true, "flac": true, "libopus": true}, Decoders: map[string]bool{"vc1": true, "pcm": true, "truehd": true, "aac": true, "hevc": true, "h264": true, "eac3": true}, Filters: map[string]bool{"zscale": true, "tonemap": true, "bwdif": true}}
}
func allOutputClient() ClientProfile {
	p := appleTVProfile()
	p.Video = append(p.Video, ClientVideoCodec{Codec: "av1", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr", "hdr10", "hlg"}, MaxWidth: 4096, MaxHeight: 2160})
	p.Audio = append(p.Audio, ClientAudioCodec{Codec: "opus", MaxChannels: 8})
	for i := range p.Transports {
		if p.Transports[i].Transport == TransportHLSFMP4 {
			p.Transports[i].Video = append(p.Transports[i].Video, "av1")
			p.Transports[i].Audio = append(p.Transports[i].Audio, "opus")
		}
	}
	return p
}
func TestEveryRegisteredOutputHasAnExecutablePlannerTarget(t *testing.T) {
	facts := plannerToolchain()
	outputs := decoder.AvailableConversionOutputs(facts)
	client := allOutputClient()
	plan := func(source *DeliverySource, outputs ConversionOutputs) DeliveryPlan {
		t.Helper()
		p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: facts, Outputs: &outputs, FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
		if err != nil {
			t.Fatal(err)
		}
		if p.ConversionTarget == nil {
			t.Fatal("no target")
		}
		if _, _, _, err := p.CodecArgs(); err != nil {
			t.Fatal(err)
		}
		return p
	}
	for _, output := range outputs.Video {
		for _, dr := range output.Ranges {
			t.Run(output.Codec+strconv.Itoa(output.BitDepth)+dr, func(t *testing.T) {
				source := videoSource("mkv", "vc1", assets.StreamDetail{BitDepth: output.BitDepth, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: dr}, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
				selected := plan(source, ConversionOutputs{Video: []ConversionVideoOutput{output}, Audio: outputs.Audio})
				if selected.OutputVideoCodec != output.Codec || selected.OutputBitDepth != output.BitDepth || selected.OutputRange != dr {
					t.Fatal(selected.ConversionTarget)
				}
			})
		}
	}
	for _, output := range outputs.Audio {
		for channels := range output.Bitrates {
			t.Run(output.Codec+strconv.Itoa(channels), func(t *testing.T) {
				local := client
				local.Audio = []ClientAudioCodec{{Codec: output.Codec, MaxChannels: 8}}
				local.AudioOutput.MaxChannels = 8
				saved := client
				client = local
				defer func() { client = saved }()
				source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "pcm", Channels: channels, StreamDetail: assets.StreamDetail{BitDepth: 24, SampleRate: 48000}})
				selected := plan(source, ConversionOutputs{Video: outputs.Video, Audio: []ConversionAudioOutput{output}})
				if selected.OutputAudioCodec != output.Codec || selected.AudioChannels != channels {
					t.Fatal(selected.ConversionTarget)
				}
			})
		}
	}
}

func TestCodecQualityLaddersAndOriginalQualityMode(t *testing.T) {
	for _, codec := range []string{"h264", "hevc"} {
		for _, step := range []struct {
			height, ceiling, rate int
			id                    string
		}{{360, 1000000, 1000000, "240p"}, {480, 2000000, 1500000, "480p"}, {720, 4000000, 3000000, "720p-high"}, {1080, 8000000, 8000000, "1080p-low"}, {1440, 12000000, 12000000, "1080p-high"}, {2160, 20000000, 20000000, "2160p"}} {
			t.Run(codec+step.id, func(t *testing.T) {
				client := allOutputClient()
				client.Video = []ClientVideoCodec{{Codec: codec, BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr"}, MaxWidth: 4096, MaxHeight: 2160}}
				source := videoSource("mkv", "vc1", assets.StreamDetail{BitDepth: 8, Width: 3840, Height: 2160, FrameRate: 24, DynamicRange: "sdr"}, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
				p, err := planDelivery(DeliveryInput{Source: source, Client: client, Server: plannerToolchain(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: step.id, Kind: QualityAutomatic, MaxVideoHeight: step.height, MaxVideoBitrateBPS: step.ceiling, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
				want := step.rate
				if codec == "hevc" {
					want = want * 7 / 10
				}
				if err != nil || p.ConversionTarget == nil || p.ConversionTarget.RateControl != "bitrate" || p.VideoBitrateBPS != want {
					t.Fatalf("%+v %v want %d", p.ConversionTarget, err, want)
				}
			})
		}
	}
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	for _, ceiling := range []int{0, 2000000} {
		p, err := planDelivery(DeliveryInput{Source: source, Client: allOutputClient(), Server: plannerToolchain(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, MaxVideoBitrateBPS: ceiling, AllowHDR: true}, Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true})
		if err != nil || p.ConversionTarget == nil || p.ConversionTarget.RateControl != "quality" || p.ConversionTarget.VideoBitrateBPS != ceiling {
			t.Fatal(p.ConversionTarget, err)
		}
	}
}

func TestLosslessRateAndPassthroughSurvivePictureConversion(t *testing.T) {
	client := allOutputClient()
	client.AudioPassthrough = []ClientAudioPassthrough{{Codec: "truehd", MaxChannels: 8}}
	for i := range client.Transports {
		if client.Transports[i].Transport == TransportHLSFMP4 {
			client.Transports[i].Audio = append(client.Transports[i].Audio, "truehd")
		}
	}
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "truehd", Channels: 8, StreamDetail: assets.StreamDetail{SampleRate: 96000, BitDepth: 24, BitRate: 9000000}})
	p := decide(t, source, client, func(in *DeliveryInput) { in.Server = plannerToolchain(); in.FMP4Output = true })
	if p.AudioAction != "copy" || !p.AudioPassthrough || p.OutputContainer != "fmp4_hls" {
		t.Fatal(p)
	}
	_, args, _, err := p.CodecArgs()
	if err != nil || !strings.Contains(strings.Join(args, " "), "-c:a copy") {
		t.Fatal(args, err)
	}
	client.AudioPassthrough = nil
	p = decide(t, source, client, func(in *DeliveryInput) { in.Server = plannerToolchain(); in.FMP4Output = true })
	if p.OutputAudioCodec != "flac" || p.AudioChannels != 8 || !p.AudioLossless || p.ConversionTarget.AudioSampleRate != 96000 || p.ConversionTarget.AudioBitDepth != 24 || p.AudioBitrateBPS != 0 {
		t.Fatal(p.ConversionTarget)
	}
}

func TestSubtitleOriginalsBesideConvertedStreamsAvoidBurn(t *testing.T) {
	native := appleTVProfile()
	mpv := mpvProfile(native)
	doc := documentOf(native, mpv)
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	// Restrict mpv's direct file support to force a converted stream.
	doc.Engines.MPV.Transports = doc.Engines.MPV.Transports[1:]
	for _, kind := range []string{SubtitleASS, SubtitlePGS, SubtitleVobSub, SubtitleDVB} {
		doc.EngineSetting = EngineSettingMPV
		p := planDocument(t, source, doc, func(in *DeliveryInput) { in.SubtitleKind = kind; in.SubtitleBesideStream = true })
		if SubtitleNeedsBurnIn(doc.Profile(p.Engine), kind, p.Mode, true) {
			t.Fatal(kind, p)
		}
		if !SubtitleNeedsBurnIn(doc.Profile(EngineNative), kind, p.Mode, true) {
			t.Fatal("native claimed an absent renderer", kind)
		}
	}
	// Automatic prefers mpv's renderer for a playable original over a burn.
	doc = documentOf(native, mpv)
	p := planDocument(t, videoSource("mp4", "h264", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 2}), doc, func(in *DeliveryInput) { in.SubtitleKind = SubtitlePGS; in.SubtitleBesideStream = true })
	if p.Engine != EngineMPV {
		t.Fatal(p.Engine)
	}
}

func TestDolbyVisionCopyAndSDRBaseTargetsStayAccurate(t *testing.T) {
	video := hdr10Detail
	video.DynamicRange = assets.RangeDolbyVision
	video.DolbyVisionProfile = 5
	video.CodecTag = "dvh1"
	source := videoSource("mp4", "hevc", video, SourceAudio{Index: 1, Codec: "truehd", Channels: 8, StreamDetail: assets.StreamDetail{SampleRate: 48000, BitsPerSample: 24}})
	p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.Server = plannerToolchain(); in.FMP4Output = true })
	if p.Strategy != DeliveryAudioConversion || p.ConversionTarget == nil || !p.ConversionTarget.CopyDolbyVision || p.ConversionTarget.DynamicRange != assets.RangeDolbyVision || hevcSampleEntry(&p) != "dvh1" {
		t.Fatal(p.ConversionTarget)
	}
	_, args, _, err := p.CodecArgs()
	if err != nil || strings.Contains(strings.Join(args, " "), "-bsf:v") {
		t.Fatal(args, err)
	}
	video.DolbyVisionProfile = 8
	video.DolbyVisionCompatibility = 2
	source = videoSource("mp4", "hevc", video, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	p = decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.Server = plannerToolchain(); in.FMP4Output = true; in.BurnIn = true })
	if p.ToneMap || p.OutputRange != assets.RangeSDR || p.ConversionTarget.SourceRange != assets.RangeSDR || p.ConversionTarget.CopyDolbyVision {
		t.Fatal(p.ConversionTarget)
	}
}

func TestAudioSampleDepthCeilingUsesMpvBeforeReducingSamples(t *testing.T) {
	native := appleTVProfile()
	for i := range native.Audio {
		if native.Audio[i].Codec == "flac" {
			native.Audio[i].MaxBitDepth = 16
		}
	}
	source := videoSource("mp4", "h264", avcDetail, SourceAudio{Index: 1, Codec: "flac", Channels: 2, StreamDetail: assets.StreamDetail{BitsPerSample: 24, SampleRate: 48000}})
	doc := documentOf(native, mpvProfile(native))
	p := planDocument(t, source, doc, nil)
	if p.Engine != EngineMPV || p.Strategy != DeliveryOriginal {
		t.Fatal(p.Engine, p.Strategy)
	}
	doc.EngineSetting = EngineSettingNative
	p = planDocument(t, source, doc, nil)
	if p.AudioLossless || p.ConversionTarget == nil || p.ConversionTarget.AudioBitDepth != 16 || p.OutputAudioCodec != "flac" {
		t.Fatal(p.ConversionTarget, p.AudioLossless)
	}
}
