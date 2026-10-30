package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// planDocument plans a source for a whole capability document.
func planDocument(t *testing.T, source *DeliverySource, doc CapabilityDocument, mutate func(*DeliveryInput)) DeliveryPlan {
	t.Helper()
	policy := openPolicy()
	height := 0
	if source.Video != nil {
		height = source.Video.Height
	}
	in := DeliveryInput{Source: source, Device: &doc, Policy: policy, Target: autoTarget(policy, height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true}
	if mutate != nil {
		mutate(&in)
	}
	p, err := planDelivery(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// The plan names the engine. On automatic the native player keeps every title
// it can play as it is or with a plain repackaging; mpv takes a title only
// when the native player would need a converted picture, a lossy audio
// conversion or burned-in subtitles and mpv can keep the original streams.
func TestAutomaticEngineChoice(t *testing.T) {
	apple := appleTVProfile()
	both := documentOf(apple, mpvProfile(apple))
	eac3 := SourceAudio{Codec: "eac3", Channels: 6, Language: "eng", Default: true}
	dts := SourceAudio{Codec: "dts", Channels: 6, Language: "eng", Default: true}
	vp9 := assets.StreamDetail{Profile: "Profile 0", BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 24, DynamicRange: assets.RangeSDR, BitRate: 6_000_000}

	for _, test := range []struct {
		name     string
		source   *DeliverySource
		mutate   func(*DeliveryInput)
		engine   string
		strategy DeliveryStrategy
	}{
		{"a file the native player plays as it is", videoSource("mp4", "hevc", hdr10Detail, eac3), nil, EngineNative, DeliveryOriginal},
		{"a file the native player only needs repackaged", videoSource("mkv", "hevc", hdr10Detail, eac3), nil, EngineNative, DeliveryCopyRemux},
		{"a picture the native player would need converted", videoSource("mkv", "vp9", vp9, eac3), nil, EngineMPV, DeliveryOriginal},
		{"sound the native player would need converted to a lossy codec", videoSource("mkv", "hevc", hdr10Detail, dts), func(in *DeliveryInput) { outputs := lossyOnlyTestOutputs(); in.Outputs = &outputs }, EngineMPV, DeliveryOriginal},
		{"image subtitles the native player cannot draw", videoSource("mp4", "hevc", hdr10Detail, eac3), func(in *DeliveryInput) { in.SubtitleKind = SubtitlePGS }, EngineMPV, DeliveryOriginal},
		{"text subtitles cost nothing", videoSource("mp4", "hevc", hdr10Detail, eac3), func(in *DeliveryInput) { in.SubtitleKind = "" }, EngineNative, DeliveryOriginal},
		{"a subtitle the viewer wants burned in whatever the engine", videoSource("mp4", "hevc", hdr10Detail, eac3), func(in *DeliveryInput) { in.SubtitleKind = SubtitleBurnAlways }, EngineNative, DeliveryOriginal},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := planDocument(t, test.source, both, test.mutate)
			if p.Engine != test.engine || p.Strategy != test.strategy || p.Trace.Client.Engine != test.engine {
				t.Fatalf("engine %s by %s, want %s by %s: %v", p.Engine, p.Strategy, test.engine, test.strategy, p.Trace.Routes)
			}
			if !contains(p.Trace.Notes, NoteEngineAutomatic) {
				t.Fatal("the trace does not say the engine was chosen automatically", p.Trace.Notes)
			}
		})
	}

	t.Run("mpv repackages when native would convert the picture", func(t *testing.T) {
		limited := mpvProfile(apple)
		for i := range limited.Transports {
			if limited.Transports[i].Transport == TransportDirect {
				limited.Transports[i].Containers = []string{"mp4"}
			}
		}
		p := planDocument(t, videoSource("mkv", "vp9", vp9, eac3), documentOf(apple, limited), nil)
		if p.Engine != EngineMPV || p.Strategy != DeliveryCopyRemux || p.VideoAction != "copy" || p.AudioAction != "copy" {
			t.Fatal(p.Engine, p.Strategy, p.VideoAction, p.AudioAction)
		}
	})

	t.Run("mpv conversion does not displace a valid native conversion", func(t *testing.T) {
		p := planDocument(t, videoSource("mkv", "vp9", vp9, eac3), both, func(in *DeliveryInput) {
			in.ExcludedByEngine = map[string]map[DeliveryStrategy]RouteRejection{EngineMPV: {
				DeliveryOriginal:  {Code: "decode_error"},
				DeliveryCopyRemux: {Code: "decode_error"},
			}}
		})
		if p.Engine != EngineNative || p.Strategy != DeliveryVideoConversion {
			t.Fatal(p.Engine, p.Strategy)
		}
	})

	t.Run("the viewer's setting is honoured", func(t *testing.T) {
		native := both
		native.EngineSetting = EngineSettingNative
		if p := planDocument(t, videoSource("mkv", "vp9", vp9, eac3), native, nil); p.Engine != EngineNative || p.Strategy != DeliveryVideoConversion {
			t.Fatal("native setting", p.Engine, p.Strategy)
		}
		mpv := both
		mpv.EngineSetting = EngineSettingMPV
		if p := planDocument(t, videoSource("mp4", "hevc", hdr10Detail, eac3), mpv, nil); p.Engine != EngineMPV || p.Strategy != DeliveryOriginal {
			t.Fatal("mpv setting", p.Engine, p.Strategy)
		}
		// A device that says mpv but carries none is planned for its own player.
		none := documentOf(apple)
		none.EngineSetting = EngineSettingMPV
		if p := planDocument(t, videoSource("mp4", "hevc", hdr10Detail, eac3), none, nil); p.Engine != EngineNative {
			t.Fatal("no mpv engine", p.Engine)
		}
	})

	t.Run("a re-plan stays in the engine that is playing", func(t *testing.T) {
		p := planDocument(t, videoSource("mkv", "vp9", vp9, eac3), both, func(in *DeliveryInput) { in.Engine = EngineNative })
		if p.Engine != EngineNative || p.Strategy != DeliveryVideoConversion {
			t.Fatal(p.Engine, p.Strategy)
		}
		p = planDocument(t, videoSource("mp4", "hevc", hdr10Detail, eac3), both, func(in *DeliveryInput) { in.Engine, in.BurnIn = EngineMPV, true })
		if p.Engine != EngineMPV || p.Strategy != DeliveryVideoConversion || !contains(p.ReasonCodes, ReasonSubtitleBurnIn) {
			t.Fatal(p.Engine, p.Strategy, p.ReasonCodes)
		}
	})

	t.Run("a route that failed in one engine is still open in the other", func(t *testing.T) {
		failed := map[string]map[DeliveryStrategy]RouteRejection{EngineMPV: {DeliveryOriginal: {Code: "decode_error"}}}
		p := planDocument(t, videoSource("mkv", "vp9", vp9, eac3), both, func(in *DeliveryInput) { in.ExcludedByEngine = failed })
		if p.Engine != EngineMPV || p.Strategy != DeliveryCopyRemux {
			t.Fatal("mpv should try repackaging without repeating its failed original", p.Engine, p.Strategy)
		}
		failed = map[string]map[DeliveryStrategy]RouteRejection{EngineNative: {DeliveryOriginal: {Code: "decode_error"}}}
		p = planDocument(t, videoSource("mp4", "hevc", hdr10Detail, eac3), both, func(in *DeliveryInput) { in.ExcludedByEngine = failed })
		if p.Engine != EngineNative || p.Strategy != DeliveryCopyRemux {
			t.Fatal("a native failure should repackage for the native player first", p.Engine, p.Strategy)
		}
	})

	t.Run("music and remote sources never leave the native player", func(t *testing.T) {
		song := &DeliverySource{ID: "song", Container: "flac", Duration: 200, Audio: []SourceAudio{{Index: 0, Codec: "flac", Channels: 2}}, DetailKnown: true}
		if p := planDocument(t, song, both, nil); p.Engine != EngineNative {
			t.Fatal(p.Engine)
		}
	})
}

// Whether a styled or image subtitle is burned in depends on the engine and on
// whether the track can reach it.
func TestSubtitleNeedsBurnIn(t *testing.T) {
	apple := appleTVProfile()
	mpv := mpvProfile(apple)
	for _, test := range []struct {
		name         string
		engine       ClientProfile
		kind, mode   string
		besideStream bool
		want         bool
	}{
		{"no subtitle", apple, "", "hls", false, false},
		{"a kind the engine does not draw", apple, SubtitleASS, "direct", false, true},
		{"a kind the engine draws, inside the file it is playing", mpv, SubtitleASS, "direct", false, false},
		{"a kind the engine draws, but the file is being repackaged", mpv, SubtitlePGS, "hls", false, true},
		{"a kind the engine draws, handed over beside the stream", mpv, SubtitlePGS, "hls", true, false},
		{"burned in on request", mpv, SubtitleBurnAlways, "direct", true, true},
	} {
		if got := SubtitleNeedsBurnIn(test.engine, test.kind, test.mode, test.besideStream); got != test.want {
			t.Errorf("%s: %v, want %v", test.name, got, test.want)
		}
	}
}

// Audio a receiver decodes is left alone when the device can hand it over, and
// only then.
func TestAudioPassthroughFollowsTheOutputRoute(t *testing.T) {
	shield := func(route string, overrides CapabilityOverrides) ClientProfile {
		p := chromeProfile()
		p.Client.Family = "androidtv"
		p.AudioOutput = ClientAudioOutput{Route: route, MaxChannels: 8}
		p.AudioPassthrough = []ClientAudioPassthrough{{Codec: "truehd", MaxChannels: 8, ObjectAudio: []string{"atmos"}}, {Codec: "dts", MaxChannels: 8}, {Codec: "eac3", MaxChannels: 8}, {Codec: "ac3", MaxChannels: 6}}
		for i := range p.Transports {
			p.Transports[i].Audio = append(p.Transports[i].Audio, "truehd", "dts", "eac3", "ac3")
			if p.Transports[i].Transport == TransportDirect {
				p.Transports[i].Containers = append(p.Transports[i].Containers, "mkv")
			}
		}
		p.Overrides = overrides
		return documentOf(p).Profile(EngineNative)
	}
	atmos := SourceAudio{Codec: "truehd", Channels: 8}
	atmos.ObjectAudio = "atmos"
	source := videoSource("mkv", "h264", avcDetail, atmos)
	p := decide(t, source, shield("hdmi", CapabilityOverrides{}), nil)
	if p.Strategy != DeliveryOriginal || !p.AudioPassthrough || !contains(p.Trace.Notes, NoteAudioPassthrough) {
		t.Fatal("TrueHD was not passed to the receiver", p.Strategy, p.Trace.Routes)
	}
	for name, client := range map[string]ClientProfile{
		"the sound goes to the device's own speaker": shield("speaker", CapabilityOverrides{}),
		"the route is not known":                     shield("", CapabilityOverrides{}),
		"the viewer turned passthrough off":          shield("hdmi", CapabilityOverrides{DisablePassthrough: true}),
		"the viewer asked for stereo":                shield("hdmi", CapabilityOverrides{ForceStereo: true}),
	} {
		p = decide(t, source, client, nil)
		if p.AudioAction != "convert" || p.AudioPassthrough {
			t.Fatalf("%s: audio is %s", name, p.AudioAction)
		}
		// The route closes the passthrough list; an override removes it, and
		// the note says which.
		if !rejected(p, DeliveryOriginal, RejectPassthrough) && !rejected(p, DeliveryOriginal, RejectAudioCodec) {
			t.Fatalf("%s: the trace does not say why the track was not taken: %v", name, p.Trace.Routes)
		}
	}
}

// A device that tone-maps HDR itself is given HDR even when its screen is not
// HDR; one that does not is not; and the viewer can turn HDR off.
func TestDeviceToneMappingIsDeclared(t *testing.T) {
	source := videoSource("mp4", "hevc", hdr10Detail, SourceAudio{Codec: "aac", Channels: 2})
	sdrScreen := func(toneMaps bool, overrides CapabilityOverrides) ClientProfile {
		p := appleTVProfile()
		p.Display.DynamicRanges = []string{"sdr"}
		p.ToneMapsHDR, p.Overrides = toneMaps, overrides
		return documentOf(p).Profile(EngineNative)
	}
	p := decide(t, source, sdrScreen(true, CapabilityOverrides{}), nil)
	if p.Strategy != DeliveryOriginal || p.ToneMap || !contains(p.Trace.Notes, NoteDeviceToneMaps) {
		t.Fatal("a device that tone-maps was converted for", p.Strategy, p.Trace.Notes)
	}
	p = decide(t, source, sdrScreen(false, CapabilityOverrides{}), nil)
	if p.Strategy != DeliveryVideoConversion || !p.ToneMap || !rejected(p, DeliveryOriginal, RejectDisplayRange) {
		t.Fatal("HDR was sent to a screen that cannot show it", p.Strategy, p.Trace.Routes)
	}
	// A display that says nothing is not held against the decoder.
	silent := appleTVProfile()
	silent.Display.DynamicRanges = nil
	if p = decide(t, source, documentOf(silent).Profile(EngineNative), nil); p.Strategy != DeliveryOriginal {
		t.Fatal(p.Strategy, p.Trace.Routes)
	}
	p = decide(t, source, sdrScreen(true, CapabilityOverrides{DisableHDR: true}), nil)
	if p.Strategy != DeliveryVideoConversion || !p.ToneMap || !contains(p.Trace.Notes, NoteOverrideNoHDR) {
		t.Fatal("the viewer's HDR override was ignored", p.Strategy, p.Trace.Notes)
	}
}

// Forcing stereo plans every title as two channels decoded by the device.
func TestForceStereoOverride(t *testing.T) {
	stereo := appleTVProfile()
	stereo.Overrides = CapabilityOverrides{ForceStereo: true}
	stereo = documentOf(stereo).Profile(EngineNative)
	p := decide(t, videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "eac3", Channels: 6}), stereo, nil)
	if p.Strategy != DeliveryAudioConversion || p.OutputAudioCodec != "aac" || p.AudioChannels != 2 || p.Downmix != DownmixITULimited || !contains(p.Trace.Notes, NoteOverrideStereo) {
		t.Fatal(p.Strategy, p.OutputAudioCodec, p.AudioChannels, p.Trace.Notes)
	}
	// A stereo title is untouched.
	if p = decide(t, videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2}), stereo, nil); p.Strategy != DeliveryOriginal {
		t.Fatal(p.Strategy)
	}
}

// A losslessly coded surround track is not quietly turned into 384 kbps AAC
// when the device takes something better.
func TestLosslessAudioKeepsTheBestTargetTheDeviceTakes(t *testing.T) {
	truehd := SourceAudio{Codec: "truehd", Channels: 8}
	dtsMA := SourceAudio{Codec: "dts", Channels: 6}
	dtsMA.Profile = "DTS-HD MA"
	// With no fragmented MP4 producer, keep all eight channels in AAC rather
	// than reduce them to Dolby 5.1.
	p := decide(t, videoSource("mkv", "h264", avcDetail, truehd), appleTVProfile(), nil)
	if p.AudioAction != "convert" || p.OutputAudioCodec != "aac" || p.AudioChannels != 8 || p.AudioBitrateBPS != 768_000 || p.AudioLossless {
		t.Fatal(p.OutputAudioCodec, p.AudioChannels, p.AudioBitrateBPS)
	}
	if p = decide(t, videoSource("mkv", "h264", avcDetail, dtsMA), appleTVProfile(), nil); p.OutputAudioCodec != "eac3" {
		t.Fatal("DTS-HD MA", p.OutputAudioCodec)
	}
	// Lossy surround still goes to multichannel AAC first.
	if p = decide(t, videoSource("mkv", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 6}), appleTVProfile(), nil); p.OutputAudioCodec != "aac" || p.AudioChannels != 6 || p.AudioBitrateBPS != 512_000 {
		t.Fatal("DTS core", p.OutputAudioCodec, p.AudioChannels, p.AudioBitrateBPS)
	}
	// A browser with stereo AAC and nothing else gets stereo AAC.
	if p = decide(t, videoSource("mkv", "h264", avcDetail, truehd), chromeProfile(), nil); p.OutputAudioCodec != "aac" || p.AudioChannels != 2 {
		t.Fatal("stereo floor", p.OutputAudioCodec, p.AudioChannels)
	}
	// A server that can write a lossless target uses it where the device
	// takes it, and the plan says nothing was lost.
	outputs := DefaultConversionOutputs()
	outputs.Audio = append([]ConversionAudioOutput{{Codec: "flac", Lossless: true, Bitrates: map[int]int{2: 0, 6: 0, 8: 0}, Transports: []string{TransportHLSFMP4}}}, outputs.Audio...)
	p = decide(t, videoSource("mkv", "h264", avcDetail, truehd), appleTVProfile(), func(in *DeliveryInput) { in.Outputs, in.FMP4Output = &outputs, true })
	if p.OutputAudioCodec != "flac" || p.AudioChannels != 8 || p.OutputContainer != "fmp4_hls" {
		// H.264 can use fragmented MP4 to retain lossless surround.
		t.Fatal("TS carrier", p.OutputAudioCodec)
	}
	hevc := hdr10Detail
	p = decide(t, videoSource("mkv", "hevc", hevc, truehd), appleTVProfile(), func(in *DeliveryInput) { in.Outputs, in.FMP4Output = &outputs, true })
	if p.Strategy != DeliveryAudioConversion || p.OutputContainer != "fmp4_hls" || p.OutputAudioCodec != "flac" || p.AudioChannels != 8 || !p.AudioLossless {
		t.Fatal("lossless target", p.Strategy, p.OutputContainer, p.OutputAudioCodec, p.AudioChannels, p.AudioLossless)
	}
	// Lossless audio is not a reason to leave the native player.
	if engineCompromises(p) {
		t.Fatal("a lossless conversion was counted as a compromise")
	}
}

// The planner picks the picture a conversion produces from what the server can
// write and the device takes: HDR is kept where both can.
func TestConversionPictureFollowsTheServersOutputs(t *testing.T) {
	vp9HDR := assets.StreamDetail{Profile: "Profile 2", BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 24, DynamicRange: assets.RangeHDR10, BitRate: 30_000_000}
	source := videoSource("mkv", "vp9", vp9HDR, SourceAudio{Codec: "aac", Channels: 2})
	// The registered converter keeps 10-bit HDR in HEVC.
	p := decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.FMP4Output = true })
	if p.Strategy != DeliveryVideoConversion || p.OutputVideoCodec != "hevc" || p.OutputRange != assets.RangeHDR10 || p.OutputBitDepth != 10 || p.ToneMap || p.OutputContainer != "fmp4_hls" {
		t.Fatal(p.OutputVideoCodec, p.OutputRange, p.OutputBitDepth, p.ToneMap, p.OutputContainer)
	}
	// A converter that writes 10-bit HEVC keeping HDR.
	outputs := DefaultConversionOutputs()
	outputs.Video = append([]ConversionVideoOutput{{Codec: "hevc", BitDepth: 10, Ranges: []string{assets.RangeHDR10, assets.RangeHLG, assets.RangeSDR}, Transports: []string{TransportHLSFMP4}}}, outputs.Video...)
	p = decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.Outputs, in.FMP4Output = &outputs, true })
	if p.OutputVideoCodec != "hevc" || p.OutputRange != assets.RangeHDR10 || p.OutputBitDepth != 10 || p.ToneMap || p.OutputContainer != "fmp4_hls" || p.Trace.Video.OutputRange != assets.RangeHDR10 {
		t.Fatal("HDR was not kept", p.OutputVideoCodec, p.OutputRange, p.ToneMap, p.OutputContainer)
	}
	// The same converter for a device with HDR turned off: HEVC, tone-mapped.
	off := appleTVProfile()
	off.Overrides = CapabilityOverrides{DisableHDR: true}
	p = decide(t, source, documentOf(off).Profile(EngineNative), func(in *DeliveryInput) { in.Outputs, in.FMP4Output = &outputs, true })
	if p.OutputVideoCodec != "hevc" || p.OutputRange != assets.RangeSDR || !p.ToneMap {
		t.Fatal("HDR off", p.OutputVideoCodec, p.OutputRange, p.ToneMap)
	}
	// A device with no HEVC falls to H.264.
	if p = decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.Outputs, in.FMP4Output = &outputs, true }); p.OutputVideoCodec != "h264" || p.OutputRange != assets.RangeSDR {
		t.Fatal("no HEVC", p.OutputVideoCodec, p.OutputRange)
	}
	// A device with fragmented MP4 HLS and no MPEG-TS can still be converted for.
	noTS := chromeProfile()
	kept := noTS.Transports[:0:0]
	for _, tr := range noTS.Transports {
		if tr.Transport != TransportHLSTS {
			kept = append(kept, tr)
		}
	}
	noTS.Transports = kept
	p = decide(t, source, noTS, func(in *DeliveryInput) { in.FMP4Output = true })
	if p.Strategy != DeliveryVideoConversion || p.OutputContainer != "fmp4_hls" {
		t.Fatal("fMP4-only device", p.Strategy, p.OutputContainer)
	}
}

// HEVC and HDR are repackaged into fragmented MP4 first; H.264 into MPEG-TS.
func TestRepackagingContainerOrder(t *testing.T) {
	both := appleTVProfile()
	for i := range both.Transports {
		if both.Transports[i].Transport == TransportHLSTS {
			both.Transports[i].Video = append(both.Transports[i].Video, "hevc")
		}
	}
	both = documentOf(both).Profile(EngineNative)
	aac := SourceAudio{Codec: "aac", Channels: 2}
	if p := decide(t, videoSource("mkv", "hevc", hdr10Detail, aac), both, func(in *DeliveryInput) { in.FMP4Output = true }); p.Strategy != DeliveryCopyRemux || p.OutputContainer != "fmp4_hls" {
		t.Fatal("HEVC", p.Strategy, p.OutputContainer)
	}
	if p := decide(t, videoSource("mkv", "h264", avcDetail, aac), both, func(in *DeliveryInput) { in.FMP4Output = true }); p.Strategy != DeliveryCopyRemux || p.OutputContainer != "mpegts_hls" {
		t.Fatal("H.264", p.Strategy, p.OutputContainer)
	}
}

// A sample entry the engine does not take is repackaged, not sent as it is.
func TestCodecTagClosesDirectPlayOnly(t *testing.T) {
	apple := appleTVProfile()
	for i := range apple.Video {
		if apple.Video[i].Codec == "hevc" {
			apple.Video[i].CodecTags = []string{"hvc1", "dvh1"}
		}
	}
	apple = documentOf(apple).Profile(EngineNative)
	detail := hdr10Detail
	detail.CodecTag = "hev1"
	p := decide(t, videoSource("mp4", "hevc", detail, SourceAudio{Codec: "aac", Channels: 2}), apple, func(in *DeliveryInput) { in.FMP4Output = true })
	if p.Strategy != DeliveryCopyRemux || !rejected(p, DeliveryOriginal, RejectCodecTag) {
		t.Fatal(p.Strategy, p.Trace.Routes)
	}
	detail.CodecTag = "hvc1"
	if p = decide(t, videoSource("mp4", "hevc", detail, SourceAudio{Codec: "aac", Channels: 2}), apple, nil); p.Strategy != DeliveryOriginal {
		t.Fatal(p.Strategy)
	}
}

// The remembered language outranks the preferred list and yields to an
// explicit track.
func TestRememberedAudioLanguage(t *testing.T) {
	tracks := []SourceAudio{{Index: 1, Language: "eng", Default: true}, {Index: 2, Language: "jpn"}, {Index: 3, Language: "jpn", Commentary: true}}
	if a, by := chooseAudio(tracks, -1, "ja", []string{"en"}, ""); a.Index != 2 || by != "carried" {
		t.Fatal(a, by)
	}
	if a, by := chooseAudio(tracks, 1, "ja", nil, ""); a.Index != 1 || by != "explicit" {
		t.Fatal(a, by)
	}
	if a, by := chooseAudio(tracks, -1, "fr", []string{"en"}, ""); a.Index != 1 || by != "preferred_language" {
		t.Fatal(a, by)
	}
}

// A file whose bitrate nobody observed does not pass every ceiling: it is
// taken to be an ordinary file of its picture size.
func TestUnknownBitrateIsAssumedUnderACeiling(t *testing.T) {
	detail := avcDetail
	detail.BitRate = 0
	source := videoSource("mp4", "h264", detail, SourceAudio{Codec: "aac", Channels: 2})
	source.BitRate = 0
	// No ceiling: nothing to assume, the file plays as it is.
	if p := decide(t, source, chromeProfile(), nil); p.Strategy != DeliveryOriginal || contains(p.Trace.Notes, NoteBitrateAssumed) {
		t.Fatal(p.Strategy, p.Trace.Notes)
	}
	// A 1080p file under a 4 Mb/s ceiling is taken as 12 Mb/s and converted.
	p := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 4_000_000 })
	if p.Strategy != DeliveryVideoConversion || p.VideoBitrateBPS != 4_000_000 || !contains(p.Trace.Notes, NoteBitrateAssumed) || !rejected(p, DeliveryOriginal, ReasonBitrateExceedsPolicy) {
		t.Fatal(p.Strategy, p.VideoBitrateBPS, p.Trace.Notes)
	}
	// A file that was never analysed is not guessed at twice.
	unread := source
	unread.DetailKnown = false
	if p = decide(t, unread, chromeProfile(), func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 4_000_000 }); p.Strategy != DeliveryOriginal || contains(p.Trace.Notes, NoteBitrateAssumed) {
		t.Fatal(p.Strategy, p.Trace.Notes)
	}
	// A roomy ceiling leaves it alone.
	if p = decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 20_000_000 }); p.Strategy != DeliveryOriginal {
		t.Fatal(p.Strategy)
	}
	for height, want := range map[int]int64{2160: 40_000_000, 1440: 20_000_000, 1080: 12_000_000, 720: 6_000_000, 480: 3_000_000, 0: 12_000_000} {
		if got := assumedBitrate(height); got != want {
			t.Errorf("%dp is taken as %d, want %d", height, got, want)
		}
	}
}

func lossyOnlyTestOutputs() ConversionOutputs {
	out := DefaultConversionOutputs()
	audio := []ConversionAudioOutput{}
	for _, o := range out.Audio {
		if !o.Lossless {
			audio = append(audio, o)
		}
	}
	out.Audio = audio
	return out
}
