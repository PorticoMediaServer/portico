package streaming

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/mediafacts"
)

// profileNames lists the profile files in profiles/.
func profileNames(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob("profiles/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatal("no profile files", err)
	}
	var names []string
	for _, path := range paths {
		if !strings.HasSuffix(path, ".cases.json") {
			names = append(names, strings.TrimSuffix(filepath.Base(path), ".json"))
		}
	}
	return names
}

// profileDocument is one profile file's document as it is written. A file must say where
// its facts came from.
func profileDocument(t *testing.T, name string) json.RawMessage {
	t.Helper()
	var file struct {
		Source   string          `json:"source"`
		Document json.RawMessage `json:"document"`
	}
	raw, err := os.ReadFile("profiles/" + name + ".json")
	if err == nil {
		err = json.Unmarshal(raw, &file)
	}
	if err != nil {
		t.Fatal(name, err)
	}
	if strings.TrimSpace(file.Source) == "" {
		t.Fatalf("profiles/%s.json has no source note", name)
	}
	return file.Document
}

// recordedDocuments reads every profile as planning would: an app's document through the
// same check a published one gets, the server's own two as the server loads them.
func recordedDocuments(t *testing.T) map[string]CapabilityDocument {
	t.Helper()
	out := map[string]CapabilityDocument{}
	for _, name := range profileNames(t) {
		if name == "baseline" || name == "airplay" {
			profileDocument(t, name)
			out[name] = builtinDocument(name)
			continue
		}
		d, err := ParseCapabilityDocument(profileDocument(t, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		out[name] = d
	}
	return out
}

// matrixDocuments are the recorded documents that carry an mpv engine.
func matrixDocuments(t *testing.T) map[string]CapabilityDocument {
	t.Helper()
	out := recordedDocuments(t)
	for name, d := range out {
		if !d.HasEngine(EngineMPV) {
			delete(out, name)
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
				in := DeliveryInput{
					Source:             source,
					Device:             &doc,
					Policy:             policy,
					Target:             autoTarget(policy, source.Video.Height),
					Config:             DefaultDeliveryConfiguration(),
					TranscodingEnabled: true,
					FMP4Output:         true,
				}
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
			if p := plan(videoSource("mkv", "h264", avcDetail, SourceAudio{Codec: "dts", Channels: 6}),
				func(in *DeliveryInput) { outputs := lossyOnlyTestOutputs(); in.Outputs = &outputs }); p.Engine != EngineMPV ||
				p.Strategy != DeliveryOriginal {
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

// matrixProfiles are the recorded documents with one engine, as that engine.
func matrixProfiles(t *testing.T) map[string]ClientProfile {
	t.Helper()
	out := map[string]ClientProfile{}
	for name, d := range recordedDocuments(t) {
		if !d.HasEngine(EngineMPV) {
			out[name] = d.Profile(EngineNative)
		}
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

// matrixRow is one kind of file, and what must hold for it on every device. What each
// device is expected to be given is in that device's case file.
type matrixRow struct {
	source    string
	build     func() DeliverySource
	configure func(*DeliveryInput)
	check     func(*testing.T, DeliveryPlan)
}

// profileCase is one row of a case file, profiles/<name>.cases.json: how one kind of file
// is expected to reach that device.
type profileCase struct {
	File string `json:"file"`
	// Route is original, copy_remux, audio_conversion, video_conversion, or refused.
	Route DeliveryStrategy `json:"route"`
	// Audio is the codec the device is sent.
	Audio string `json:"audio,omitempty"`
	// Engine is the player chosen, for a document with two.
	Engine string `json:"engine,omitempty"`
	// Channels, Height and FrameRate are checked where a case states them.
	Channels  int     `json:"channels,omitempty"`
	Height    int     `json:"height,omitempty"`
	FrameRate float64 `json:"frameRate,omitempty"`
	// Note says why, for the person reading.
	Note string `json:"note,omitempty"`
}

// profileCases reads a profile's case file. It must name every kind of file in the
// matrix, once, in the matrix's order.
func profileCases(t *testing.T, name string, rows []matrixRow) []profileCase {
	t.Helper()
	file, err := os.Open("profiles/" + name + ".cases.json")
	if err != nil {
		t.Fatalf("profile %s has no case file: %v", name, err)
	}
	defer file.Close()
	var cases []profileCase
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cases); err != nil {
		t.Fatalf("profiles/%s.cases.json: %v", name, err)
	}
	if len(cases) != len(rows) {
		t.Fatalf("profiles/%s.cases.json has %d cases; the matrix has %d kinds of file", name, len(cases), len(rows))
	}
	for i, row := range rows {
		if cases[i].File != row.source {
			t.Fatalf("profiles/%s.cases.json case %d is %q; the matrix has %q there", name, i+1, cases[i].File, row.source)
		}
	}
	return cases
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
	build := func(container, codec string, detail mediafacts.StreamDetail, audio SourceAudio) func() DeliverySource {
		return func() DeliverySource { return *videoSource(container, codec, detail, audio) }
	}
	surround := func(codec string) SourceAudio {
		return SourceAudio{Codec: codec, Channels: 8, StreamDetail: mediafacts.StreamDetail{ObjectAudio: "atmos"}}
	}
	dv := func(profile, compatibility int, tag string) mediafacts.StreamDetail {
		d := hdr10Detail
		d.DynamicRange = mediafacts.RangeDolbyVision
		d.DolbyVisionProfile = profile
		d.DolbyVisionCompatibility = compatibility
		d.CodecTag = tag
		return d
	}
	hi10 := avc
	hi10.BitDepth = 10
	hi10.Profile = "High 10"
	av1 := hdr10Detail
	av1.DynamicRange = mediafacts.RangeSDR
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
		{source: "01_mp4_h264_aac", build: build("mp4", "h264", avc, aac)},
		{source: "02_mkv_h264_aac", build: build("mkv", "h264", avc, aac)},
		{source: "03_mkv_h264_ac3", build: build("mkv", "h264", avc, SourceAudio{Codec: "ac3", Channels: 6})},
		{source: "04_mkv_h264_dtshd", build: build("mkv", "h264", avc, SourceAudio{Codec: "dts", Channels: 8, StreamDetail: mediafacts.StreamDetail{Profile: "DTS-HD MA"}})},
		{source: "05_mkv_h264_truehd_atmos", build: build("mkv", "h264", avc, surround("truehd"))},
		{source: "06_mp4_hdr10_eac3", build: build("mp4", "hevc", hdr10Detail, SourceAudio{Codec: "eac3", Channels: 6})},
		{source: "07_mkv_hdr10_eac3", build: build("mkv", "hevc", hdr10Detail, SourceAudio{Codec: "eac3", Channels: 6})},
		{source: "08_dv_p5_dvh1", build: build("mp4", "hevc", dv(5, 0, "dvh1"), aac)},
		{source: "09_dv_p81_hvc1", build: build("mp4", "hevc", dv(8, 1, "hvc1"), aac)},
		{source: "10_dv_p81_dvh1_hdr_only", build: build("mp4", "hevc", dv(8, 1, "dvh1"), aac)},
		{source: "11_mkv_dv_p7_truehd", build: build("mkv", "hevc", dv(7, 1, ""), surround("truehd"))},
		{source: "12_mkv_av1_10_opus", build: build("mkv", "av1", av1, SourceAudio{Codec: "opus", Channels: 2})},
		{source: "13_webm_vp9_opus", build: build("webm", "vp9", vp9, SourceAudio{Codec: "opus", Channels: 2})},
		{source: "14_mkv_hi10p", build: build("mkv", "h264", hi10, aac)},
		{source: "15_interlaced_mpeg2_mp2", build: build("ts", "mpeg2video", mpeg2, SourceAudio{Codec: "mp2", Channels: 2})},
		{source: "16_vc1_mkv", build: build("mkv", "vc1", vc1, aac)},
		{source: "17_4k_h264_level52", build: build("mp4", "h264", uhd, aac)},
		{source: "18_120fps", build: build("mp4", "h264", fast, aac)},
		{source: "19_rotated_mp4", build: build("mp4", "h264", rotated, aac)},
		{source: "20_rotated_mkv", build: build("mkv", "h264", rotated, aac)},
		{
			source: "21_nonfirst_audio",
			build: func() DeliverySource {
				return *videoSource("mp4", "h264", avc, aac, SourceAudio{Codec: "aac", Channels: 2, Language: "fra"})
			},
			configure: func(in *DeliveryInput) { in.HasAudioChoice = true; in.AudioStream = 2 },
			check: func(t *testing.T, p DeliveryPlan) {
				if p.AudioStream != 2 {
					t.Fatal("wrong audio stream", p.AudioStream)
				}
			},
		},
		{
			source:    "22_bitrate_above_lane",
			build:     build("mp4", "h264", avc, aac),
			configure: func(in *DeliveryInput) { in.Target.MaxVideoBitrateBPS = 1_000_000 },
			check: func(t *testing.T, p DeliveryPlan) {
				if p.VideoBitrateBPS > 1_000_000 {
					t.Fatal("bitrate ceiling bypassed", p.VideoBitrateBPS)
				}
			},
		},
		{source: "23_owner_disables_conversion", build: build("mkv", "h264", hi10, aac), configure: func(in *DeliveryInput) { in.TranscodingEnabled = false }},
		{source: "24_flac_music", build: func() DeliverySource {
			return DeliverySource{ID: "source", Container: "flac", Duration: 100, DetailKnown: true, Audio: []SourceAudio{{Codec: "flac", Channels: 2, Index: 0}}}
		}},
		{
			// The device's own player has said it could not play the file as it is.
			source:    "25_original_decode_error",
			build:     build("mp4", "h264", avc, aac),
			configure: func(in *DeliveryInput) { in.Excluded = map[DeliveryStrategy]RouteRejection{O: {Code: "decode_error"}} },
			check: func(t *testing.T, p DeliveryPlan) {
				if !rejected(p, O, RejectClientFailure) {
					t.Fatal("failure not recorded")
				}
			},
		},
	}
}

func matrixInput(row matrixRow, client ClientProfile) DeliveryInput {
	source := row.build()
	height := 0
	if source.Video != nil {
		height = source.Video.Height
	}
	policy := openPolicy()
	in := DeliveryInput{
		Source:             &source,
		Client:             client,
		Server:             matrixToolchain(),
		Policy:             policy,
		Target:             autoTarget(policy, height),
		Config:             DefaultDeliveryConfiguration(),
		TranscodingEnabled: true,
		FMP4Output:         true,
	}
	if row.configure != nil {
		row.configure(&in)
	}
	return in
}

// matrixDocumentInput is matrixInput for a whole document: one engine as that engine, two
// engines with the choice between them left to the planner. A failure the row names is the
// platform player's.
func matrixDocumentInput(row matrixRow, doc CapabilityDocument) DeliveryInput {
	in := matrixInput(row, doc.Profile(EngineNative))
	if doc.HasEngine(EngineMPV) {
		in.Client, in.Device = ClientProfile{}, &doc
		if in.Excluded != nil {
			in.ExcludedByEngine, in.Excluded = map[string]map[DeliveryStrategy]RouteRejection{EngineNative: in.Excluded}, nil
		}
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
	for _, c := range []struct{ source, device string }{{"10_dv_p81_dvh1_hdr_only", "safari_hdr"}, {"10_dv_p81_dvh1_hdr_only", "ultra"}, {"11_mkv_dv_p7_truehd", "safari_hdr"},
		{"11_mkv_dv_p7_truehd", "appletv_dv"}} {
		p, err := plan(c.source, c.device)
		if err != nil || p.Strategy != V {
			t.Errorf("%s on %s: want its base layer converted, got %s %v", c.source, c.device, p.Strategy, err)
		}
	}
}

// Every profile file plans the 25 kinds of file the way its case file says. The cases are
// what a person on that device gets; a change to a rule or to a profile that moves one
// shows here by name.
func TestClientDeclaredTablesDriveDeliveryMatrix(t *testing.T) {
	documents := recordedDocuments(t)
	rows := matrixRows()
	if len(rows) != 25 {
		t.Fatal("the matrix is 25 kinds of file")
	}
	cases := map[string][]profileCase{}
	for name := range documents {
		cases[name] = profileCases(t, name, rows)
	}
	for i, row := range rows {
		t.Run(row.source, func(t *testing.T) {
			for name, doc := range documents {
				t.Run(name, func(t *testing.T) {
					want := cases[name][i]
					p, e := planDelivery(matrixDocumentInput(row, doc))
					if want.Route == refused {
						var refusal ErrDeliveryRefused
						if !errors.As(e, &refusal) || refusal.Code != ReasonTranscodeDisabledOwner {
							t.Fatalf("want owner refusal got %+v %v", p, e)
						}
						return
					}
					if e != nil {
						t.Fatal(e)
					}
					if p.Strategy != want.Route {
						t.Fatalf("want %s got %s; routes=%+v", want.Route, p.Strategy, p.Trace.Routes)
					}
					if p.OutputAudioCodec != want.Audio {
						t.Errorf("audio codec want %s got %s", want.Audio, p.OutputAudioCodec)
					}
					if want.Engine != "" && p.Engine != want.Engine {
						t.Errorf("player want %s got %s", want.Engine, p.Engine)
					}
					if doc.HasEngine(EngineMPV) && want.Engine == "" {
						t.Error("a case for a document with two players must name the player")
					}
					// Surround keeps every channel the output carries, and a stereo output
					// is given two, never a drop to mono or a layout it cannot play.
					if want.Channels != 0 && p.AudioChannels != want.Channels {
						t.Errorf("channels want %d got %d", want.Channels, p.AudioChannels)
					}
					if want.Height != 0 && p.TargetHeight != want.Height {
						t.Errorf("height want %d got %d", want.Height, p.TargetHeight)
					}
					if want.FrameRate != 0 && p.MaxFrameRate != want.FrameRate {
						t.Errorf("frame rate cap want %v got %v", want.FrameRate, p.MaxFrameRate)
					}
					// What holds on every device, whatever its case says.
					if p.Strategy == A && p.AudioAction != "convert" {
						t.Error("audio was not converted")
					}
					if p.Strategy == R && p.AudioAction != "copy" {
						t.Error("audio was not copied")
					}
					if (row.source[:2] == "07" || row.source[:2] == "10" || row.source[:2] == "11" || row.source[:2] == "12") && (p.Strategy == R || p.Strategy == A) {
						if p.OutputContainer != "fmp4_hls" {
							t.Error("copied video needs fMP4", p.OutputContainer)
						}
					}
					if (row.source[:2] == "10" || row.source[:2] == "11") && (p.Strategy == R || p.Strategy == A) {
						if p.Trace.Video.OutputRange != mediafacts.RangeHDR10 || !strings.HasPrefix(videoCodecString(&p), "hvc1.") {
							t.Error("HDR base not retagged", p.Trace.Video, videoCodecString(&p))
						}
					}
					if row.source[:2] == "15" && p.Strategy == V && !p.Deinterlace {
						t.Error("interlaced conversion lacks deinterlacing")
					}
					if row.check != nil {
						row.check(t, p)
					}
				})
			}
		})
	}
}

// ClientIdentity is there to explain a decision afterwards. Nothing in planning may read
// it: a device is planned from what it says it plays, never from what it is called, so a
// new platform needs a profile and no rule of its own.
func TestPlanningNeverReadsWhoTheDeviceSaysItIs(t *testing.T) {
	for name, doc := range recordedDocuments(t) {
		renamed := doc
		renamed.Client = ClientIdentity{Family: "another"}
		for _, row := range matrixRows() {
			named, e1 := planDelivery(matrixDocumentInput(row, doc))
			other, e2 := planDelivery(matrixDocumentInput(row, renamed))
			if named.Trace != nil && other.Trace != nil {
				named.Trace.Client, other.Trace.Client = ClientSummary{}, ClientSummary{}
			}
			if fmt.Sprint(e1) != fmt.Sprint(e2) || !reflect.DeepEqual(named, other) {
				t.Errorf("%s on %s is planned differently under another name", row.source, name)
			}
		}
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
