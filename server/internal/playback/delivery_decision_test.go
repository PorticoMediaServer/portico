package playback

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// Device documents used across the decision tests. They are deliberately close
// to what the real clients publish, so the matrix below reads as "what happens
// when this file is played on that device".
func appleTVProfile() ClientProfile {
	p := ClientProfile{
		Version: ClientProfileVersion, Evidence: EvidenceMixed, EngineName: "avplayer",
		Client:  ClientIdentity{Family: "apple", Platform: "tvos", Model: "AppleTV14,1"},
		Display: ClientDisplay{Width: 3840, Height: 2160, DynamicRanges: []string{"sdr", "hdr10", "hlg", "dolby_vision"}, MaxFrameRate: 60},
		Video: []ClientVideoCodec{
			{Codec: "h264", BitDepths: []int{8}, MaxWidth: 4096, MaxHeight: 2304, MaxFrameRate: 60, DynamicRanges: []string{"sdr"}},
			{Codec: "hevc", BitDepths: []int{8, 10}, MaxWidth: 4096, MaxHeight: 2304, MaxFrameRate: 60, DynamicRanges: []string{"sdr", "hdr10", "hlg", "dolby_vision"}, DolbyVisionProfiles: []int{5, 8}},
		},
		Audio: []ClientAudioCodec{
			{Codec: "aac", MaxChannels: 8}, {Codec: "mp3", MaxChannels: 2}, {Codec: "ac3", MaxChannels: 6},
			{Codec: "eac3", MaxChannels: 8, ObjectAudio: []string{"atmos"}}, {Codec: "alac", MaxChannels: 8}, {Codec: "flac", MaxChannels: 8},
		},
		AudioOutput: ClientAudioOutput{Route: "hdmi", MaxChannels: 8, Spatial: true},
		Transports: []ClientTransport{
			{Transport: TransportDirect, Containers: []string{"mp4", "m4v", "mov"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "mp3", "ac3", "eac3", "alac", "flac"}},
			{Transport: TransportDirect, Containers: []string{"m4a", "mp3", "flac", "wav"}, Audio: []string{"aac", "mp3", "alac", "flac"}},
			{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac", "mp3", "ac3", "eac3"}},
			{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "mp3", "ac3", "eac3", "alac", "flac"}},
		},
		SubtitleRendering:      []string{SubtitleText},
		EmbeddedAudioSwitching: true,
	}
	p = documentOf(p).Profile(EngineNative)
	p.Revision = "apple-tv-test"
	return p
}

// documentOf is the capability document a device with this native engine
// (and, when given, this mpv engine) would publish.
func documentOf(native ClientProfile, mpv ...ClientProfile) CapabilityDocument {
	engine := func(p ClientProfile) *EngineCapabilities {
		evidence := p.Evidence
		if evidence == "" || evidence == EvidenceBuiltin {
			evidence = EvidenceDeclared
		}
		return &EngineCapabilities{Name: p.EngineName, Evidence: evidence, Video: p.Video, Audio: p.Audio, AudioPassthrough: p.AudioPassthrough, Transports: p.Transports, SubtitleRendering: p.SubtitleRendering, ToneMapsHDR: p.ToneMapsHDR, EmbeddedAudioSwitching: p.EmbeddedAudioSwitching}
	}
	d := CapabilityDocument{Version: CapabilityDocumentVersion, Client: native.Client, Overrides: native.Overrides, Display: native.Display, AudioOutput: native.AudioOutput, MaxBitrateBPS: native.MaxBitrateBPS, AudioDecode: native.AudioDecode, Engines: CapabilityEngines{Native: engine(native)}}
	if d.Client.Family == "" {
		d.Client.Family = "test"
	}
	if len(mpv) > 0 {
		d.Engines.MPV = engine(mpv[0])
	}
	if err := d.Accept(); err != nil {
		panic(err)
	}
	return d
}

// mpvProfile is an mpv engine beside a device's native one: it plays any
// container and codec in software and draws every subtitle kind.
func mpvProfile(native ClientProfile) ClientProfile {
	video := []string{"h264", "hevc", "vp9", "av1", "mpeg2video", "mpeg4", "vc1"}
	audio := []string{"aac", "mp3", "ac3", "eac3", "dts", "truehd", "flac", "opus", "vorbis", "alac", "pcm"}
	p := native
	p.Engine, p.EngineName, p.Evidence = EngineMPV, "mpv", EvidenceDeclared
	p.Video, p.Audio = nil, nil
	for _, codec := range video {
		p.Video = append(p.Video, ClientVideoCodec{Codec: codec, BitDepths: []int{8, 10}, MaxWidth: 4096, MaxHeight: 2304, DynamicRanges: []string{"sdr", "hdr10", "hlg"}, Interlaced: true})
	}
	for _, codec := range audio {
		p.Audio = append(p.Audio, ClientAudioCodec{Codec: codec, MaxChannels: 8})
	}
	p.AudioPassthrough = nil
	p.Transports = []ClientTransport{
		{Transport: TransportDirect, Containers: []string{"mp4", "m4v", "mov", "mkv", "webm", "avi", "ts", "m2ts"}, Video: video, Audio: audio},
		{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "mp3", "ac3", "eac3"}},
		{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264", "hevc", "av1", "vp9"}, Audio: []string{"aac", "ac3", "eac3", "flac", "opus"}},
	}
	p.SubtitleRendering = []string{SubtitleText, SubtitleASS, SubtitlePGS, SubtitleVobSub, SubtitleDVB}
	p.ToneMapsHDR, p.EmbeddedAudioSwitching = true, true
	return documentOf(native, p).Profile(EngineMPV)
}

func chromeProfile() ClientProfile {
	p := ClientProfile{
		Version: ClientProfileVersion, Evidence: EvidenceProbed, EngineName: "hls.js",
		Client:  ClientIdentity{Family: "web", Platform: "chrome"},
		Display: ClientDisplay{Width: 1920, Height: 1080, DynamicRanges: []string{"sdr"}, MaxFrameRate: 60},
		Video: []ClientVideoCodec{
			{Codec: "h264", BitDepths: []int{8}, MaxWidth: 4096, MaxHeight: 2160, MaxFrameRate: 60, DynamicRanges: []string{"sdr"}},
			{Codec: "vp9", BitDepths: []int{8, 10}, MaxWidth: 4096, MaxHeight: 2160, DynamicRanges: []string{"sdr"}},
			{Codec: "av1", BitDepths: []int{8, 10}, MaxWidth: 4096, MaxHeight: 2160, DynamicRanges: []string{"sdr"}},
		},
		Audio:       []ClientAudioCodec{{Codec: "aac", MaxChannels: 6}, {Codec: "mp3", MaxChannels: 2}, {Codec: "opus", MaxChannels: 6}, {Codec: "flac", MaxChannels: 6}, {Codec: "vorbis", MaxChannels: 6}},
		AudioOutput: ClientAudioOutput{Route: "speaker", MaxChannels: 2},
		Transports: []ClientTransport{
			{Transport: TransportDirect, Containers: []string{"mp4", "m4v", "mov"}, Video: []string{"h264", "vp9", "av1"}, Audio: []string{"aac", "mp3", "opus", "flac"}},
			{Transport: TransportDirect, Containers: []string{"webm"}, Video: []string{"vp9", "av1"}, Audio: []string{"opus", "vorbis"}},
			{Transport: TransportDirect, Containers: []string{"mp3", "m4a", "flac", "ogg", "opus", "wav"}, Audio: []string{"aac", "mp3", "opus", "flac", "vorbis"}},
			{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac", "mp3"}},
			{Transport: TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264"}, Audio: []string{"aac", "mp3", "flac", "opus"}},
		},
		SubtitleRendering: []string{SubtitleText},
	}
	p = documentOf(p).Profile(EngineNative)
	p.Revision = "chrome-test"
	return p
}

func videoSource(container, codec string, detail assets.StreamDetail, audio ...SourceAudio) *DeliverySource {
	for i := range audio {
		audio[i].Ordinal, audio[i].Index = i, i+1
	}
	return &DeliverySource{ID: "source", Container: container, Duration: 3600, BitRate: detail.BitRate, Video: &SourceVideo{Codec: codec, StreamDetail: detail}, Audio: audio, DetailKnown: true}
}

func decide(t *testing.T, source *DeliverySource, client ClientProfile, mutate func(*DeliveryInput)) DeliveryPlan {
	t.Helper()
	policy := openPolicy()
	height := 0
	if source.Video != nil {
		height = source.Video.Height
	}
	in := DeliveryInput{Source: source, Client: client, Policy: policy, Target: autoTarget(policy, height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true}
	if mutate != nil {
		mutate(&in)
	}
	p, err := planDelivery(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Trace == nil || len(p.Trace.Routes) == 0 {
		t.Fatal("a plan was published without its decision trace")
	}
	chosen := 0
	for _, r := range p.Trace.Routes {
		if r.Chosen {
			chosen++
			if !r.Admissible || r.Route != string(p.Strategy) {
				t.Fatalf("trace marks %s chosen but the plan is %s", r.Route, p.Strategy)
			}
		}
		if !r.Admissible && len(r.Rejections) == 0 {
			t.Fatalf("route %s was rejected without a reason", r.Route)
		}
	}
	if chosen != 1 {
		t.Fatalf("trace marks %d routes chosen", chosen)
	}
	return p
}

func rejected(p DeliveryPlan, route DeliveryStrategy, code string) bool {
	r := routeEvaluation(p.Trace, route)
	return r != nil && !r.Admissible && hasRejection(r.Rejections, code)
}

var hdr10Detail = assets.StreamDetail{Profile: "Main 10", Level: 153, BitDepth: 10, Width: 3840, Height: 2160, FrameRate: 23.976, DynamicRange: assets.RangeHDR10, BitRate: 40_000_000}
var avcDetail = assets.StreamDetail{Profile: "High", Level: 41, BitDepth: 8, Width: 1920, Height: 1080, FrameRate: 23.976, DynamicRange: assets.RangeSDR, BitRate: 8_000_000}

func TestDecisionMatrixAcrossDevices(t *testing.T) {
	eac3 := SourceAudio{Codec: "eac3", Channels: 6, Language: "eng", Default: true}
	t.Run("hdr10 hevc mkv on apple tv copies into fragmented mp4", func(t *testing.T) {
		p := decide(t, videoSource("mkv", "hevc", hdr10Detail, eac3), appleTVProfile(), func(in *DeliveryInput) { in.FMP4Output = true })
		if p.Strategy != DeliveryCopyRemux || p.OutputContainer != "fmp4_hls" || p.ToneMap || p.AudioAction != "copy" {
			t.Fatal(p)
		}
		if !rejected(p, DeliveryOriginal, RejectContainer) {
			t.Fatal("the trace does not say the container was the obstacle", p.Trace.Routes)
		}
	})
	t.Run("without fragmented mp4 output the same file converts, tone maps and keeps its audio", func(t *testing.T) {
		p := decide(t, videoSource("mkv", "hevc", hdr10Detail, eac3), appleTVProfile(), nil)
		if p.Strategy != DeliveryVideoConversion || !p.ToneMap || p.AudioAction != "copy" || p.OutputAudioCodec != "eac3" {
			t.Fatal(p)
		}
	})
	t.Run("hdr10 hevc mp4 plays directly on apple tv and converts for chrome", func(t *testing.T) {
		source := videoSource("mp4", "hevc", hdr10Detail, eac3)
		if p := decide(t, source, appleTVProfile(), nil); p.Strategy != DeliveryOriginal {
			t.Fatal(p)
		}
		p := decide(t, source, chromeProfile(), nil)
		if p.Strategy != DeliveryVideoConversion || !p.ToneMap || p.AudioAction != "convert" || p.AudioChannels != 2 || p.Downmix != DownmixITULimited {
			t.Fatal(p)
		}
		if !rejected(p, DeliveryOriginal, RejectVideoCodec) || !rejected(p, DeliveryOriginal, RejectAudioCodec) {
			t.Fatal(p.Trace.Routes)
		}
	})
	t.Run("dolby vision profile 5 needs a dolby vision device", func(t *testing.T) {
		detail := hdr10Detail
		detail.DynamicRange, detail.DolbyVisionProfile, detail.DolbyVisionCompatibility, detail.CodecTag = assets.RangeDolbyVision, 5, 0, "dvh1"
		source := videoSource("mp4", "hevc", detail, eac3)
		if p := decide(t, source, appleTVProfile(), nil); p.Strategy != DeliveryOriginal {
			t.Fatal(p)
		}
		noDV := appleTVProfile()
		noDV.Video[1].DolbyVisionProfiles = []int{}
		policy := openPolicy()
		p, err := planDelivery(DeliveryInput{Source: source, Client: noDV, Policy: policy, Target: autoTarget(policy, source.Video.Height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true})
		var refused ErrDeliveryRefused
		if !errors.As(err, &refused) || !rejected(p, DeliveryOriginal, RejectDolbyVision) {
			t.Fatal(p, err)
		}
	})
	t.Run("dolby vision 8.1 falls back to its hdr10 layer", func(t *testing.T) {
		detail := hdr10Detail
		detail.DynamicRange, detail.DolbyVisionProfile, detail.DolbyVisionCompatibility, detail.CodecTag = assets.RangeDolbyVision, 8, 1, "hvc1"
		noDV := appleTVProfile()
		noDV.Video[1].DolbyVisionProfiles = []int{}
		if p := decide(t, videoSource("mp4", "hevc", detail, eac3), noDV, nil); p.Strategy != DeliveryOriginal {
			t.Fatal(p, p.Trace.Routes)
		}
		detail.CodecTag = "dvh1"
		p := decide(t, videoSource("mp4", "hevc", detail, eac3), noDV, func(in *DeliveryInput) {
			in.FMP4Output = true
			in.Server = plannerToolchain()
			in.Server.BitstreamFilters = map[string]bool{"dovi_rpu": true}
		})
		if p.Strategy != DeliveryCopyRemux || !rejected(p, DeliveryOriginal, RejectDolbyVisionEntry) {
			t.Fatal(p, p.Trace.Routes)
		}
	})
	t.Run("ten bit h264 is not an eight bit decoder's problem to discover", func(t *testing.T) {
		detail := avcDetail
		detail.Profile, detail.BitDepth = "High 10", 10
		p := decide(t, videoSource("mp4", "h264", detail, SourceAudio{Codec: "aac", Channels: 2}), chromeProfile(), nil)
		if p.Strategy != DeliveryVideoConversion || !rejected(p, DeliveryOriginal, RejectVideoBitDepth) || !rejected(p, DeliveryCopyRemux, RejectVideoBitDepth) {
			t.Fatal(p, p.Trace.Routes)
		}
	})
	t.Run("surround is kept when the output route can play it", func(t *testing.T) {
		source := videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 6})
		p := decide(t, source, chromeProfile(), nil)
		if p.Strategy != DeliveryAudioConversion || p.AudioChannels != 2 || p.Downmix == "" || p.VideoAction != "copy" {
			t.Fatal(p)
		}
		surround := chromeProfile()
		surround.AudioOutput.MaxChannels = 6
		p = decide(t, source, surround, nil)
		if p.AudioChannels != 6 || p.AudioBitrateBPS != 512_000 || p.Downmix != "" {
			t.Fatal(p)
		}
		if _, args, _, err := p.CodecArgs(); err != nil || (!strings.Contains(strings.Join(args, " "), "-c:a aac -ac 6") || !strings.Contains(strings.Join(args, " "), "-b:a 512000")) {
			t.Fatal(args, err)
		}
	})
	t.Run("a preferred language on the second track leaves the original behind", func(t *testing.T) {
		source := videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: "fra"})
		p := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.PreferredAudioLanguages = []string{"fr"} })
		if p.Strategy != DeliveryCopyRemux || p.AudioStream != 2 || p.Trace.Audio.ChosenBy != "preferred_language" || !rejected(p, DeliveryOriginal, RejectAudioTrack) {
			t.Fatal(p, p.Trace.Routes)
		}
		// A device that can switch tracks inside the file keeps the original.
		if p = decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.PreferredAudioLanguages = []string{"fre"} }); p.Strategy != DeliveryOriginal || p.AudioStream != 2 {
			t.Fatal(p)
		}
		// A known original-language match excludes commentary, even the default.
		source = videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2, Language: "eng", Commentary: true, Default: true}, SourceAudio{Codec: "aac", Channels: 2, Language: "eng"})
		if p = decide(t, source, appleTVProfile(), func(in *DeliveryInput) { in.OriginalLanguage = "en" }); p.AudioStream != 2 {
			t.Fatal("commentary chosen automatically", p.Trace.Audio)
		}
	})
	t.Run("a network ceiling applies to the original too", func(t *testing.T) {
		detail := avcDetail
		detail.BitRate = 40_000_000
		source := videoSource("mp4", "h264", detail, SourceAudio{Codec: "aac", Channels: 2})
		cfg := DefaultDeliveryConfiguration()
		cfg.MaxVideoBitrateBPS = 8_000_000
		lane := ResolveDeliveryPolicy(nil, NetworkRemote, LocalityRemote, "cellular", cfg)
		p := decide(t, source, chromeProfile(), func(in *DeliveryInput) { in.Policy, in.Target = lane, autoTarget(lane, 1080) })
		if p.Strategy != DeliveryVideoConversion || p.VideoBitrateBPS != 8_000_000 || !rejected(p, DeliveryOriginal, ReasonBitrateExceedsPolicy) || !contains(p.ReasonCodes, ReasonBitrateExceedsPolicy) {
			t.Fatal(p, p.Trace.Routes)
		}
	})
	t.Run("interlaced mpeg-2 is deinterlaced", func(t *testing.T) {
		detail := assets.StreamDetail{Profile: "Main", BitDepth: 8, Width: 720, Height: 576, FrameRate: 25, Interlaced: true, DynamicRange: assets.RangeSDR}
		p := decide(t, videoSource("ts", "mpeg2video", detail, SourceAudio{Codec: "mp2", Channels: 2}), chromeProfile(), nil)
		if p.Strategy != DeliveryVideoConversion || !p.Deinterlace {
			t.Fatal(p)
		}
		if _, args, _, err := p.CodecArgs(); err != nil || !strings.Contains(strings.Join(args, " "), "yadif=") {
			t.Fatal(args, err)
		}
	})
	t.Run("a high frame rate source is capped at what the decoder takes", func(t *testing.T) {
		detail := avcDetail
		detail.FrameRate = 119.88
		p := decide(t, videoSource("mkv", "hevc", detail, SourceAudio{Codec: "aac", Channels: 2}), chromeProfile(), nil)
		if p.MaxFrameRate != 60 {
			t.Fatal(p)
		}
	})
	t.Run("a route the device reported broken is not offered again", func(t *testing.T) {
		source := videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2})
		p := decide(t, source, chromeProfile(), func(in *DeliveryInput) {
			in.Excluded = map[DeliveryStrategy]RouteRejection{DeliveryOriginal: {Code: "decode_error"}}
		})
		if p.Strategy != DeliveryCopyRemux || !rejected(p, DeliveryOriginal, RejectClientFailure) {
			t.Fatal(p, p.Trace.Routes)
		}
		p = decide(t, source, chromeProfile(), func(in *DeliveryInput) {
			in.Excluded = map[DeliveryStrategy]RouteRejection{DeliveryOriginal: {Code: "decode_error"}, DeliveryCopyRemux: {Code: "decode_error"}, DeliveryAudioConversion: {Code: "decode_error"}}
		})
		if p.Strategy != DeliveryVideoConversion {
			t.Fatal(p)
		}
	})
	t.Run("lossless music plays as it is where the device says so", func(t *testing.T) {
		source := &DeliverySource{ID: "song", Container: "flac", Duration: 200, Audio: []SourceAudio{{Codec: "flac", Channels: 2}}, DetailKnown: true}
		if p := decide(t, source, chromeProfile(), nil); p.Strategy != DeliveryOriginal || p.Reason != ReasonDirectAudioCompatible {
			t.Fatal(p)
		}
		if p := decide(t, source, BaselineClientProfile(), nil); p.Strategy != DeliveryAudioConversion || p.VideoAction != "none" {
			t.Fatal(p)
		}
	})
	t.Run("rotated video is never copied into a container that drops the rotation", func(t *testing.T) {
		detail := avcDetail
		detail.Rotation = 90
		p := decide(t, videoSource("mkv", "h264", detail, SourceAudio{Codec: "aac", Channels: 2}), chromeProfile(), nil)
		if p.Strategy != DeliveryVideoConversion || !rejected(p, DeliveryCopyRemux, RejectRotation) {
			t.Fatal(p, p.Trace.Routes)
		}
	})
}

func TestDecisionTraceIsBoundedAndPathFree(t *testing.T) {
	p := decide(t, videoSource("mkv", "hevc", hdr10Detail, SourceAudio{Codec: "truehd", Channels: 8, StreamDetail: assets.StreamDetail{ObjectAudio: "atmos"}}), chromeProfile(), nil)
	raw, err := json.Marshal(p.Trace)
	if err != nil || len(raw) > 8<<10 {
		t.Fatal(len(raw), err)
	}
	if strings.Contains(string(raw), "source\"") || strings.Contains(string(raw), "/") && strings.Contains(string(raw), "Users") {
		t.Fatal("trace carries an identifier or a path", string(raw))
	}
	if p.Trace.Audio.ObjectAudio != "atmos" || p.Trace.Client.Family != "web" {
		t.Fatal(p.Trace)
	}
}

func TestChooseAudioLanguageFolding(t *testing.T) {
	tracks := []SourceAudio{{Index: 1, Language: "ger"}, {Index: 2, Language: "en-US"}, {Index: 3, Language: "jpn", Default: true}}
	for want, index := range map[string]int{"de": 1, "deu": 1, "eng": 2, "en": 2, "ja": 3} {
		if a, by := chooseAudio(tracks, -1, "", []string{want}, ""); a.Index != index || by != "preferred_language" {
			t.Fatal(want, a, by)
		}
	}
	if a, by := chooseAudio(tracks, -1, "", []string{"sv"}, ""); a.Index != 3 || by != "default" {
		t.Fatal(a, by)
	}
	if a, by := chooseAudio(tracks, 2, "", []string{"de"}, ""); a.Index != 2 || by != "explicit" {
		t.Fatal(a, by)
	}
}

func TestCapabilityDocumentParsing(t *testing.T) {
	raw, _ := json.Marshal(documentOf(chromeProfile(), mpvProfile(chromeProfile())))
	d, err := ParseCapabilityDocument(raw)
	if err != nil || d.Revision == "" || d.Client.Family != "web" || !d.HasEngine(EngineMPV) || d.EngineSetting != EngineSettingAutomatic {
		t.Fatal(d, err)
	}
	again, _ := ParseCapabilityDocument(raw)
	if again.Revision != d.Revision {
		t.Fatal("equal documents produced different revisions")
	}
	native := `"engines":{"native":{"evidence":"probed"}}`
	for name, body := range map[string]string{
		"version":           `{"version":1,"client":{"family":"web"},` + native + `}`,
		"no native engine":  `{"version":2,"client":{"family":"web"},"engines":{}}`,
		"wrong type":        `{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"probed","video":"everything"}}}`,
		"family":            `{"version":2,"client":{"family":"Web Browser!"},` + native + `}`,
		"evidence":          `{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"trust me"}}}`,
		"builtin evidence":  `{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"builtin"}}}`,
		"range":             `{"version":2,"client":{"family":"web"},"display":{"dynamicRanges":["hdr11"]},` + native + `}`,
		"transport":         `{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"probed","transports":[{"transport":"rtsp"}]}}}`,
		"engine setting":    `{"version":2,"client":{"family":"web"},"engineSetting":"vlc",` + native + `}`,
		"subtitle renderer": `{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"probed","subtitleRendering":["teletext"]}}}`,
	} {
		if _, err := ParseCapabilityDocument([]byte(body)); err == nil {
			t.Fatal("accepted an invalid document:", name)
		}
	}
	if _, err := ParseCapabilityDocument([]byte(strings.Repeat(" ", MaxCapabilityDocumentBytes+1))); err == nil {
		t.Fatal("accepted an oversized document")
	}
	// The document grows by addition: a field this server has not met is ignored.
	if _, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"web"},"futureField":{"a":1},` + native + `}`)); err != nil {
		t.Fatal("an additive field was refused", err)
	}
	// Synonyms fold to the names the scanner records for files.
	folded, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"web"},"engines":{"native":{"evidence":"declared","video":[{"codec":"mpeg2"},{"codec":"h265"}],"audio":[{"codec":"ec-3"}],"transports":[{"transport":"hls_ts","video":["mpeg2"],"audio":["ec-3"]}]}}}`))
	if err != nil || folded.Engines.Native.Video[0].Codec != "mpeg2video" || folded.Engines.Native.Video[1].Codec != "hevc" || folded.Engines.Native.Audio[0].Codec != "eac3" || folded.Engines.Native.Transports[0].Video[0] != "mpeg2video" || folded.Engines.Native.Transports[0].Audio[0] != "eac3" {
		t.Fatal(folded.Engines.Native, err)
	}
	// The minimal valid document plans like a device that can play nothing directly.
	minimal, err := ParseCapabilityDocument([]byte(`{"version":2,"client":{"family":"web"},` + native + `}`))
	if err != nil {
		t.Fatal(err)
	}
	source := videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2})
	policy := openPolicy()
	if _, err = planDelivery(DeliveryInput{Source: source, Device: &minimal, Policy: policy, Target: autoTarget(policy, 1080), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true}); err == nil {
		t.Fatal("a device with no transports was given a route")
	}
	// An engine view a plan stored reads back as the same engine.
	view, _ := json.Marshal(d.Profile(EngineMPV))
	if back, err := ParseClientProfile(view); err != nil || back.Engine != EngineMPV || !back.RendersSubtitle(SubtitlePGS) {
		t.Fatal(back, err)
	}
}

// A device that declares HDR10+ (and not HDR10 on its own) shows the HDR10
// base picture with the dynamic metadata (VIDEO §3 VS-2a).
func TestHDR10PlusDeclarationCoversTheHDR10Base(t *testing.T) {
	detail := hdr10Detail
	detail.HDR10Plus = true
	source := videoSource("mp4", "hevc", detail, SourceAudio{Index: 1, Codec: "eac3", Channels: 6})
	client := appleTVProfile()
	client.Display.DynamicRanges = []string{"sdr", "hdr10plus"}
	for i := range client.Video {
		if client.Video[i].Codec == "hevc" {
			client.Video[i].DynamicRanges = []string{"sdr", "hdr10plus"}
		}
	}
	if p := decide(t, source, client, nil); p.Strategy != DeliveryOriginal {
		t.Fatalf("an HDR10+ declaration did not cover the HDR10 base: %+v", p)
	}
	// Without the dynamic metadata the same declaration is not HDR10: the
	// picture is not delivered as HDR, and converting on the server is the
	// only route left.
	plain := videoSource("mp4", "hevc", hdr10Detail, SourceAudio{Index: 1, Codec: "eac3", Channels: 6})
	in := DeliveryInput{Source: plain, Client: client, Policy: openPolicy(), Target: autoTarget(openPolicy(), plain.Video.Height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true}
	p, err := planDelivery(in)
	if err != nil {
		t.Fatal(err)
	}
	if p.Strategy == DeliveryOriginal || p.OutputRange == assets.RangeHDR10 || p.OutputRange == "hdr10plus" {
		t.Fatalf("a plain HDR10 picture was delivered as HDR on an HDR10+ only declaration: %+v", p)
	}
}
