package playback

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"strings"
	"testing"
)

// matrixDocuments reads the declared documents that carry an mpv engine.
func matrixDocuments(t *testing.T) map[string]CapabilityDocument {
	t.Helper()
	raw, e := os.ReadFile("testdata/client-profiles.json")
	if e != nil {
		t.Fatal(e)
	}
	var documents map[string]json.RawMessage
	if e = json.Unmarshal(raw, &documents); e != nil {
		t.Fatal(e)
	}
	out := map[string]CapabilityDocument{}
	for name, raw := range documents {
		d, e := ParseCapabilityDocument(raw)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if d.HasEngine(EngineMPV) {
			out[name] = d
		}
	}
	return out
}

// The client's own mpv engine table, beside a real native table, takes exactly
// the titles the native player would have needed converted.
func TestDeclaredMpvEnginesTakeWhatTheNativePlayerCannot(t *testing.T) {
	documents := matrixDocuments(t)
	if len(documents) < 2 {
		t.Fatal("the declared tables carry no two-engine document")
	}
	vp9 := avcDetail
	vp9.Profile = "Profile 0"
	for name, doc := range documents {
		t.Run(name, func(t *testing.T) {
			plan := func(source *DeliverySource, mutate func(*DeliveryInput)) DeliveryPlan {
				policy := openPolicy()
				in := DeliveryInput{Source: source, Device: &doc, Policy: policy, Target: autoTarget(policy, source.Video.Height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true}
				if mutate != nil {
					mutate(&in)
				}
				p, err := planDelivery(in)
				if err != nil {
					t.Fatal(err)
				}
				return p
			}
			aac := SourceAudio{Codec: "aac", Channels: 2}
			if p := plan(videoSource("mp4", "h264", avcDetail, aac), nil); p.Engine != EngineNative || p.Strategy != DeliveryOriginal {
				t.Fatal("an ordinary file left the native player", p.Engine, p.Strategy)
			}
			if p := plan(videoSource("mkv", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 6}), func(in *DeliveryInput) { outputs := lossyOnlyTestOutputs(); in.Outputs = &outputs }); p.Engine != EngineMPV || p.Strategy != DeliveryOriginal {
				t.Fatal("DTS in Matroska", p.Engine, p.Strategy, p.Trace.Routes)
			}
			if p := plan(videoSource("mkv", "mpeg2video", avcDetail, aac), nil); p.Engine != EngineMPV || p.Strategy != DeliveryOriginal {
				t.Fatal("MPEG-2", p.Engine, p.Strategy, p.Trace.Routes)
			}
			if p := plan(videoSource("mp4", "h264", avcDetail, aac), func(in *DeliveryInput) { in.SubtitleKind = SubtitleASS }); p.Engine != EngineMPV || p.Strategy != DeliveryOriginal {
				t.Fatal("styled subtitles", p.Engine, p.Strategy)
			}
		})
	}
}

func matrixProfiles(t *testing.T) map[string]ClientProfile {
	t.Helper()
	raw, e := os.ReadFile("testdata/client-profiles.json")
	if e != nil {
		t.Fatal(e)
	}
	var documents map[string]json.RawMessage
	if e = json.Unmarshal(raw, &documents); e != nil {
		t.Fatal(e)
	}
	out := map[string]ClientProfile{"baseline": BaselineClientProfile()}
	for name, raw := range documents {
		d, e := ParseCapabilityDocument(raw)
		if e != nil {
			t.Fatalf("%s: %v", name, e)
		}
		if d.HasEngine(EngineMPV) {
			// Two-engine documents are covered by matrixDocuments: the matrix
			// below is one engine's routes.
			continue
		}
		out[name] = d.Profile(EngineNative)
	}
	return out
}

const (
	O                        = DeliveryOriginal
	R                        = DeliveryCopyRemux
	A                        = DeliveryAudioConversion
	V                        = DeliveryVideoConversion
	refused DeliveryStrategy = "refused"
)

type matrixRow struct {
	source    string
	build     func() DeliverySource
	want      map[string]DeliveryStrategy
	wantAudio map[string]string // output codec, independent of the selected route
	configure func(*DeliveryInput)
	check     func(*testing.T, string, DeliveryPlan)
}

func routes(defaultRoute DeliveryStrategy, overrides ...string) map[string]DeliveryStrategy {
	out := map[string]DeliveryStrategy{"*": defaultRoute}
	for _, group := range overrides {
		parts := strings.SplitN(group, ":", 2)
		for _, name := range strings.Fields(parts[1]) {
			out[name] = DeliveryStrategy(parts[0])
		}
	}
	return out
}
func audioCodecs(defaultCodec string, overrides ...string) map[string]string {
	out := map[string]string{"*": defaultCodec}
	for _, group := range overrides {
		parts := strings.SplitN(group, ":", 2)
		for _, name := range strings.Fields(parts[1]) {
			out[name] = parts[0]
		}
	}
	return out
}

// Converted sound: a device that plays FLAC over the chosen transport is given
// FLAC, so a conversion is never a second lossy encode, in as many channels
// as the source has and the device's output carries (two for a phone or a
// browser, six for Safari's, eight for an Apple TV on HDMI). A device without
// FLAC there is given AAC. A track the device already plays (Opus on Android)
// is copied while only the picture is converted.
func matrixRows() []matrixRow {
	aac := SourceAudio{Codec: "aac", Channels: 2, Default: true}
	avc := avcDetail
	avc.Level = 31
	avc.Width = 1280
	avc.Height = 720
	build := func(container, codec string, detail assets.StreamDetail, audio SourceAudio) func() DeliverySource {
		return func() DeliverySource { return *videoSource(container, codec, detail, audio) }
	}
	surround := func(codec string) SourceAudio {
		return SourceAudio{Codec: codec, Channels: 8, StreamDetail: assets.StreamDetail{ObjectAudio: "atmos"}}
	}
	dv := func(profile, compatibility int, tag string) assets.StreamDetail {
		d := hdr10Detail
		d.DynamicRange = assets.RangeDolbyVision
		d.DolbyVisionProfile = profile
		d.DolbyVisionCompatibility = compatibility
		d.CodecTag = tag
		return d
	}
	hi10 := avc
	hi10.BitDepth = 10
	hi10.Profile = "High 10"
	av1 := hdr10Detail
	av1.DynamicRange = assets.RangeSDR
	av1.Profile = "Main"
	av1.Level = 12
	vp9 := avc
	vp9.Profile = "Profile 0"
	vp9.Level = 0
	mpeg2 := avc
	mpeg2.Profile = "Main"
	mpeg2.Level = 0
	mpeg2.Interlaced = true
	vc1 := avc
	vc1.Profile = "Advanced"
	vc1.Level = 0
	uhd := avc
	uhd.Width = 3840
	uhd.Height = 2160
	uhd.Level = 52
	uhd.FrameRate = 60
	fast := avc
	fast.FrameRate = 120
	rotated := avc
	rotated.Rotation = 90
	return []matrixRow{
		{source: "01_mp4_h264_aac", build: build("mp4", "h264", avc, aac), want: routes(O), wantAudio: audioCodecs("aac")},
		{source: "02_mkv_h264_aac", build: build("mkv", "h264", avc, aac), want: routes(R, "original:android androidtv firetv webos tizen shield_avr"), wantAudio: audioCodecs("aac")},
		// Apple decodes AC3/EAC3 locally even with a stereo sink; passthrough-only
		// clients require an active multichannel route. Losslessly coded surround
		// goes to Dolby Digital Plus where the device decodes it to a surround output.
		{source: "03_mkv_h264_ac3", build: build("mkv", "h264", avc, SourceAudio{Codec: "ac3", Channels: 6}), want: routes(A, "copy_remux:ios tvos safari_hdr appletv_dv", "original:shield_avr"), wantAudio: audioCodecs("aac", "ac3:ios tvos safari_hdr appletv_dv shield_avr", "flac:android androidtv chromium firetv")},
		{source: "04_mkv_h264_dtshd", build: build("mkv", "h264", avc, SourceAudio{Codec: "dts", Channels: 8, StreamDetail: assets.StreamDetail{Profile: "DTS-HD MA"}}), want: routes(A, "original:shield_avr"), wantAudio: audioCodecs("aac", "dts:shield_avr", "flac:android androidtv chromium firetv ios tvos safari_hdr appletv_dv")},
		{source: "05_mkv_h264_truehd_atmos", build: build("mkv", "h264", avc, surround("truehd")), want: routes(A, "original:shield_avr"), wantAudio: audioCodecs("aac", "truehd:shield_avr", "flac:android androidtv chromium firetv ios tvos safari_hdr appletv_dv")},
		// Cast declares no EAC3 decoder. HDR-capable video alone cannot make this O/R.
		{source: "06_mp4_hdr10_eac3", build: build("mp4", "hevc", hdr10Detail, SourceAudio{Codec: "eac3", Channels: 6}), want: routes(V, "original:safari_hdr appletv_dv shield_avr", "audio_conversion:ultra google_tv"), wantAudio: audioCodecs("aac", "eac3:ios tvos safari_hdr appletv_dv shield_avr", "flac:android androidtv chromium firetv")},
		{source: "07_mkv_hdr10_eac3", build: build("mkv", "hevc", hdr10Detail, SourceAudio{Codec: "eac3", Channels: 6}), want: routes(V, "copy_remux:safari_hdr appletv_dv", "original:shield_avr", "audio_conversion:ultra google_tv"), wantAudio: audioCodecs("aac", "eac3:ios tvos safari_hdr appletv_dv shield_avr", "flac:android androidtv chromium firetv")},
		{source: "08_dv_p5_dvh1", build: build("mp4", "hevc", dv(5, 0, "dvh1"), aac), want: routes(V, "original:appletv_dv shield_avr google_tv"), wantAudio: audioCodecs("aac")},
		{source: "09_dv_p81_hvc1", build: build("mp4", "hevc", dv(8, 1, "hvc1"), aac), want: routes(V, "original:safari_hdr appletv_dv shield_avr ultra google_tv"), wantAudio: audioCodecs("aac")},
		{source: "10_dv_p81_dvh1_hdr_only", build: build("mp4", "hevc", dv(8, 1, "dvh1"), aac), want: routes(V, "original:appletv_dv shield_avr google_tv", "copy_remux:safari_hdr ultra"), wantAudio: audioCodecs("aac")},
		{source: "11_mkv_dv_p7_truehd", build: build("mkv", "hevc", dv(7, 1, ""), surround("truehd")), want: routes(V, "audio_conversion:safari_hdr appletv_dv ultra google_tv", "original:shield_avr"), wantAudio: audioCodecs("aac", "truehd:shield_avr", "flac:android androidtv chromium firetv ios tvos safari_hdr appletv_dv")},
		// The older Google TV 4K declares no AV1 decoder. Shield here has no AV1 probe.
		{source: "12_mkv_av1_10_opus", build: build("mkv", "av1", av1, SourceAudio{Codec: "opus", Channels: 2}), want: routes(V, "copy_remux:chromium"), wantAudio: audioCodecs("aac", "opus:chromium android androidtv firetv shield_avr", "flac:ios tvos safari_hdr appletv_dv")},
		// Chromecast gen 3 has no VP9 claim; 'Chromecast' is not a shared codec profile.
		{source: "13_webm_vp9_opus", build: build("webm", "vp9", vp9, SourceAudio{Codec: "opus", Channels: 2}), want: routes(V, "original:chromium android androidtv firetv webos tizen shield_avr ultra google_tv"), wantAudio: audioCodecs("aac", "opus:chromium android androidtv firetv webos tizen shield_avr ultra google_tv", "flac:ios tvos safari_hdr appletv_dv")},
		{source: "14_mkv_hi10p", build: build("mkv", "h264", hi10, aac), want: routes(V), wantAudio: audioCodecs("aac")},
		// The native Android probe does not report interlacing; TVs lack MP2 audio and MPEG2-in-HLS, so audio-only repair is unavailable.
		{source: "15_interlaced_mpeg2_mp2", build: build("ts", "mpeg2video", mpeg2, SourceAudio{Codec: "mp2", Channels: 2}), want: routes(V), wantAudio: audioCodecs("aac", "flac:android androidtv chromium firetv ios tvos safari_hdr appletv_dv shield_avr")},
		{source: "16_vc1_mkv", build: build("mkv", "vc1", vc1, aac), want: routes(V, "original:shield_avr"), wantAudio: audioCodecs("aac")},
		// Cast UHD tables stop at AVC level 5.1; a 5.2 source still needs conversion.
		{source: "17_4k_h264_level52", build: build("mp4", "h264", uhd, aac), want: routes(O, "video_conversion:android androidtv firetv chromecast ultra google_tv"), wantAudio: audioCodecs("aac")},
		{source: "18_120fps", build: build("mp4", "h264", fast, aac), want: routes(V), wantAudio: audioCodecs("aac"), check: func(t *testing.T, name string, p DeliveryPlan) {
			want := 60.0
			if name == "chromecast" {
				want = 30
			}
			if p.MaxFrameRate != want {
				t.Fatalf("fps cap %v want %v", p.MaxFrameRate, want)
			}
		}},
		{source: "19_rotated_mp4", build: build("mp4", "h264", rotated, aac), want: routes(O), wantAudio: audioCodecs("aac")},
		{source: "20_rotated_mkv", build: build("mkv", "h264", rotated, aac), want: routes(V, "original:android androidtv firetv webos tizen shield_avr"), wantAudio: audioCodecs("aac")},
		{source: "21_nonfirst_audio", build: func() DeliverySource {
			return *videoSource("mp4", "h264", avc, aac, SourceAudio{Codec: "aac", Channels: 2, Language: "fra"})
		}, want: routes(R, "original:ios tvos android androidtv firetv safari_hdr appletv_dv shield_avr"), wantAudio: audioCodecs("aac"), configure: func(in *DeliveryInput) { in.HasAudioChoice = true; in.AudioStream = 2 }, check: func(t *testing.T, _ string, p DeliveryPlan) {
			if p.AudioStream != 2 {
				t.Fatal("wrong audio stream", p.AudioStream)
			}
		}},
		{source: "22_bitrate_above_lane", build: build("mp4", "h264", avc, aac), want: routes(V), wantAudio: audioCodecs("aac"), configure: func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 1_000_000 }, check: func(t *testing.T, _ string, p DeliveryPlan) {
			if p.VideoBitrateBPS > 1_000_000 {
				t.Fatal("bitrate ceiling bypassed", p.VideoBitrateBPS)
			}
		}},
		{source: "23_owner_disables_conversion", build: build("mkv", "h264", hi10, aac), want: routes(refused), configure: func(in *DeliveryInput) { in.TranscodingEnabled = false }},
		// Declared web and Vega also lack FLAC; a browser probe, not its name, enables O.
		{source: "24_flac_music", build: func() DeliverySource {
			return DeliverySource{ID: "source", Container: "flac", Duration: 100, DetailKnown: true, Audio: []SourceAudio{{Codec: "flac", Channels: 2, Index: 0}}}
		}, want: routes(O, "audio_conversion:baseline web vega"), wantAudio: audioCodecs("flac", "aac:baseline web vega")},
		{source: "25_original_decode_error", build: build("mp4", "h264", avc, aac), want: routes(R), wantAudio: audioCodecs("aac"), configure: func(in *DeliveryInput) { in.Excluded = map[DeliveryStrategy]RouteRejection{O: {Code: "decode_error"}} }, check: func(t *testing.T, _ string, p DeliveryPlan) {
			if !rejected(p, O, RejectClientFailure) {
				t.Fatal("failure not recorded")
			}
		}},
	}
}
func matrixInput(row matrixRow, client ClientProfile) DeliveryInput {
	source := row.build()
	height := 0
	if source.Video != nil {
		height = source.Video.Height
	}
	policy := openPolicy()
	in := DeliveryInput{Source: &source, Client: client, Server: matrixToolchain(), Policy: policy, Target: autoTarget(policy, height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true}
	if row.configure != nil {
		row.configure(&in)
	}
	return in
}

// matrixToolchain is a server whose converter can do everything the planner
// may ask of it: every encoder and decoder, CPU tone mapping, Dolby Vision
// reshaping, and the filters that strip Dolby Vision down to its compatible
// base. The matrix says what each device is given when the server is not the
// limit; what a lesser server does is TestMatrixWithoutDolbyVisionTools below.
func matrixToolchain() *decoder.ToolchainFacts {
	set := func(names ...string) map[string]bool {
		out := map[string]bool{}
		for _, name := range names {
			out[name] = true
		}
		return out
	}
	return &decoder.ToolchainFacts{
		Encoders:         set("libx264", "libx265", "libsvtav1", "libaom-av1", "aac", "ac3", "eac3", "flac", "libopus"),
		Decoders:         set("h264", "hevc", "av1", "vp9", "vp8", "mpeg2video", "mpeg4", "vc1", "aac", "mp3", "mp2", "ac3", "eac3", "dca", "truehd", "flac", "alac", "opus", "vorbis", "pcm"),
		Filters:          set("zscale", "tonemap", "bwdif", "libplacebo", "scale", "overlay"),
		BitstreamFilters: set("dovi_rpu", "filter_units", "hevc_metadata"),
		Muxers:           set("mp4", "hls", "mpegts"),
		VulkanAvailable:  true,
	}
}

// A server whose converter lacks the Dolby Vision tools never shows a wrong
// picture and never sends Dolby Vision metadata to a device that cannot read
// it. A title with no compatible base layer (profile 5) is refused on a device
// without Dolby Vision rather than converted with the wrong colours; a title
// with a base layer is converted from that base instead of being repackaged
// with its Dolby Vision data stripped. Devices that play the title as it is
// are unaffected.
func TestMatrixWithoutDolbyVisionTools(t *testing.T) {
	profiles := matrixProfiles(t)
	lesser := func() *decoder.ToolchainFacts {
		f := matrixToolchain()
		delete(f.Filters, "libplacebo")
		f.VulkanAvailable = false
		f.BitstreamFilters = map[string]bool{}
		return f
	}
	plan := func(source, device string) (DeliveryPlan, error) {
		for _, row := range matrixRows() {
			if row.source == source {
				in := matrixInput(row, profiles[device])
				in.Server = lesser()
				return planDelivery(in)
			}
		}
		t.Fatal("no such row", source)
		return DeliveryPlan{}, nil
	}
	for _, device := range []string{"web", "android", "chromium", "ios", "safari_hdr", "chromecast"} {
		if p, err := plan("08_dv_p5_dvh1", device); err == nil {
			t.Errorf("%s was given profile 5 Dolby Vision by %s without the tools to reshape it", device, p.Strategy)
		}
	}
	for _, device := range []string{"appletv_dv", "shield_avr", "google_tv"} {
		if p, err := plan("08_dv_p5_dvh1", device); err != nil || p.Strategy != O {
			t.Errorf("%s plays Dolby Vision itself and needs nothing of the server: %s %v", device, p.Strategy, err)
		}
	}
	for _, c := range []struct{ source, device string }{{"10_dv_p81_dvh1_hdr_only", "safari_hdr"}, {"10_dv_p81_dvh1_hdr_only", "ultra"}, {"11_mkv_dv_p7_truehd", "safari_hdr"}, {"11_mkv_dv_p7_truehd", "appletv_dv"}} {
		p, err := plan(c.source, c.device)
		if err != nil || p.Strategy != V {
			t.Errorf("%s on %s: want its base layer converted, got %s %v", c.source, c.device, p.Strategy, err)
		}
	}
}

func TestClientDeclaredTablesDriveDeliveryMatrix(t *testing.T) {
	profiles := matrixProfiles(t)
	rows := matrixRows()
	if len(rows) != 25 {
		t.Fatal("document 52 needs all 25 rows")
	}
	for _, row := range rows {
		t.Run(row.source, func(t *testing.T) {
			for name := range row.want {
				if name != "*" {
					if _, ok := profiles[name]; !ok {
						t.Fatal("unknown route override", name)
					}
				}
			}
			for name, client := range profiles {
				t.Run(name, func(t *testing.T) {
					want, ok := row.want[name]
					if !ok {
						want = row.want["*"]
					}
					p, e := planDelivery(matrixInput(row, client))
					if want == refused {
						var refusal ErrDeliveryRefused
						if !errors.As(e, &refusal) || refusal.Code != ReasonTranscodeDisabledOwner {
							t.Fatalf("want owner refusal got %+v %v", p, e)
						}
						return
					}
					if e != nil {
						t.Fatal(e)
					}
					if p.Strategy != want {
						t.Fatalf("want %s got %s; routes=%+v", want, p.Strategy, p.Trace.Routes)
					}
					audio, ok := row.wantAudio[name]
					if !ok {
						audio = row.wantAudio["*"]
					}
					if p.OutputAudioCodec != audio {
						t.Errorf("audio codec want %s got %s", audio, p.OutputAudioCodec)
					}
					if p.Strategy == A && p.AudioAction != "convert" {
						t.Error("audio was not converted")
					}
					if p.Strategy == R && p.AudioAction != "copy" {
						t.Error("audio was not copied")
					}
					// Eight-channel sources keep every channel the output carries:
					// six through Safari, all eight on an Apple TV over HDMI.
					if (row.source[:2] == "04" || row.source[:2] == "05") && (name == "safari_hdr" && p.AudioChannels != 6 || name == "appletv_dv" && p.AudioChannels != 8) {
						t.Error("surround not retained", p.AudioChannels)
					}
					// A stereo output is given two channels, never a silent drop to mono or an unplayable layout.
					if (row.source[:2] == "03" || row.source[:2] == "04" || row.source[:2] == "05") && p.Strategy == A && (name == "android" || name == "chromium" || name == "ios") && p.AudioChannels != 2 {
						t.Error("stereo output not given stereo", p.AudioChannels)
					}
					if row.source[:2] == "17" && name == "chromecast" && p.TargetHeight != 1080 {
						t.Error("gen 3 not capped at 1080p", p.TargetHeight)
					}
					if (row.source[:2] == "07" || row.source[:2] == "10" || row.source[:2] == "11" || row.source[:2] == "12") && (p.Strategy == R || p.Strategy == A) {
						if p.OutputContainer != "fmp4_hls" {
							t.Error("copied video needs fMP4", p.OutputContainer)
						}
					}
					if (row.source[:2] == "10" || row.source[:2] == "11") && (p.Strategy == R || p.Strategy == A) {
						if p.Trace.Video.OutputRange != assets.RangeHDR10 || !strings.HasPrefix(videoCodecString(&p), "hvc1.") {
							t.Error("HDR base not retagged", p.Trace.Video, videoCodecString(&p))
						}
					}
					if row.source[:2] == "15" && p.Strategy == V && !p.Deinterlace {
						t.Error("interlaced conversion lacks deinterlacing")
					}
					if row.check != nil {
						row.check(t, name, p)
					}
				})
			}
		})
	}
}
func TestDeclaredProfilesNeverPassTrueHDOverBluetooth(t *testing.T) {
	for name, client := range matrixProfiles(t) {
		t.Run(name, func(t *testing.T) {
			client.AudioPassthrough = append(client.AudioPassthrough, ClientAudioPassthrough{Codec: "truehd", MaxChannels: 8})
			for i := range client.Transports {
				client.Transports[i].Audio = append(client.Transports[i].Audio, "truehd")
			}
			client.AudioOutput = ClientAudioOutput{Route: "bluetooth", MaxChannels: 8}
			p := decide(t, videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "truehd", Channels: 8}), client, func(in *DeliveryInput) { in.FMP4Output = true })
			if p.AudioAction != "convert" {
				t.Fatal("Bluetooth passthrough", p.AudioAction)
			}
		})
	}
}
func TestUnknownToolchainCannotPlanFragmentedCopy(t *testing.T) {
	h := &HLS{fmp4: true}
	if h.fragmentedOutputFor(nil) {
		t.Fatal("unknown toolchain advertised fMP4")
	}
	p := decide(t, videoSource("mkv", "hevc", hdr10Detail, SourceAudio{Codec: "aac", Channels: 2}), matrixProfiles(t)["appletv_dv"], func(in *DeliveryInput) { in.FMP4Output = h.fragmentedOutputFor(nil) })
	if p.Strategy != V {
		t.Fatalf("unknown muxers must convert, got %s", p.Strategy)
	}
	if !h.fragmentedOutputFor(&decoder.ToolchainFacts{Muxers: map[string]bool{"mp4": true, "hls": true}}) {
		t.Fatal("qualified muxers refused")
	}
}

func TestPeakRateClosesOriginalAndCopiedVideo(t *testing.T) {
	s := videoSource("mp4", "h264", avcDetail, SourceAudio{Codec: "aac", Channels: 2})
	s.MaxBitRate = 60_000_000
	p := decide(t, s, chromeProfile(), func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 20_000_000 })
	if p.Strategy != DeliveryVideoConversion {
		t.Fatal(p.Strategy)
	}
	s.Video.MaxBitRate = 60_000_000
	client := chromeProfile()
	client.Video[0].MaxBitrateBPS = 20_000_000
	p = decide(t, s, client, nil)
	if p.Strategy != DeliveryVideoConversion {
		t.Fatal(p.Strategy)
	}
}

// Sensitivity test: one false HEVC capability must change only the five rows
// that depended on it. This catches a matrix whose assertions stopped reading
// the exported client profiles.
func TestChromecastFalseHEVCClaimChangesExactlyAffectedRows(t *testing.T) {
	profiles := matrixProfiles(t)
	original := profiles["chromecast"]
	raw, _ := json.Marshal(original)
	var changed ClientProfile
	if e := json.Unmarshal(raw, &changed); e != nil {
		t.Fatal(e)
	}
	ultra := profiles["ultra"]
	hevc := *ultra.videoCodec("hevc")
	changed.Video = append(changed.Video, hevc)
	// A decoder is not enough: the picture also has to be shown.
	changed.Display = ultra.Display
	for i := range changed.Transports {
		if changed.Transports[i].Transport == TransportHLSFMP4 || (changed.Transports[i].Transport == TransportDirect && containsString(changed.Transports[i].Containers, "mp4")) {
			changed.Transports[i].Video = append(changed.Transports[i].Video, "hevc")
		}
	}
	want := map[string]DeliveryStrategy{"06_mp4_hdr10_eac3": A, "07_mkv_hdr10_eac3": A, "09_dv_p81_hvc1": O, "10_dv_p81_dvh1_hdr_only": R, "11_mkv_dv_p7_truehd": A}
	count := 0
	for _, row := range matrixRows() {
		before, e1 := planDelivery(matrixInput(row, original))
		after, e2 := planDelivery(matrixInput(row, changed))
		expected, affected := want[row.source]
		if !affected {
			if before.Strategy != after.Strategy || fmt.Sprint(e1) != fmt.Sprint(e2) {
				t.Errorf("unrelated row changed: %s", row.source)
			}
			continue
		}
		count++
		if e1 != nil || e2 != nil || before.Strategy == after.Strategy || after.Strategy != expected {
			t.Errorf("%s mutation did not trip expectation: %s -> %s (%v, %v)", row.source, before.Strategy, after.Strategy, e1, e2)
		}
	}
	if count != 5 {
		t.Fatal("mutation coverage incomplete", count)
	}
}
