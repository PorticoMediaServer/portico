package playbackruntime

import (
	"encoding/json"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"testing"
)

func TestLinearUsesPublishedProfileAndBoundsConvertedPicture(t *testing.T) {
	w := playback.LinearWork{ClientProfile: playback.BaselineClientProfile(), TranscodingEnabled: true}
	w.Selection.Quality.AllowLossy = true
	w.Selection.Quality.AllowHDRToSDR = true
	w.ClientProfile.Video[0].MaxFrameRate = 30
	w.ClientProfile.Video[0].MaxLevel = 41
	w.ClientProfile.Video[0].MaxHeight = 1080
	p, e := linearPlan([]byte(`{"streams":[{"codec_type":"video","codec_name":"h264","profile":"High","pix_fmt":"yuv420p","width":3840,"height":2160,"level":52,"avg_frame_rate":"60/1","field_order":"tt"},{"codec_type":"audio","codec_name":"aac","profile":"LC","channels":2,"sample_rate":"48000"}]}`), w, playback.LinearTSOnly)
	if e != nil {
		t.Fatal(e)
	}
	if !p.ConvertVideo || !p.Deinterlace || p.MaxHeight != 1080 || p.MaxFrameRate > 30 || p.Level > 41 {
		t.Fatalf("%+v", p)
	}
	w.ClientProfile.Transports = nil
	if _, e = linearPlan([]byte(`{"streams":[{"codec_type":"audio","codec_name":"aac","channels":2}]}`), w, playback.LinearTSOnly); e == nil {
		t.Fatal("admitted unavailable output transport")
	}
}

// A remote bitrate cap reaches the linear plan through the session's stored
// profile: narrowed to 3 Mbit/s, a 20 Mbit/s 1080p H.264 source converts at
// the ceiling; the un-narrowed baseline copies it, as today.
func TestLinearPlanHonorsNarrowedProfile(t *testing.T) {
	probe := []byte(`{"streams":[{"codec_type":"video","codec_name":"h264","profile":"High","pix_fmt":"yuv420p","width":1920,"height":1080,"level":40,"avg_frame_rate":"30/1","field_order":"progressive","bit_rate":"20000000"},{"codec_type":"audio","codec_name":"aac","profile":"LC","channels":2,"sample_rate":"48000"}]}`)
	work := func(p playback.ClientProfile) playback.LinearWork {
		w := playback.LinearWork{ClientProfile: p, TranscodingEnabled: true}
		w.Selection.Quality.AllowLossy = true
		return w
	}
	base, err := linearPlan(probe, work(playback.BaselineClientProfile()), playback.LinearTSOnly)
	if err != nil {
		t.Fatal(err)
	}
	if base.ConvertVideo || base.VideoBitrate != 0 {
		t.Fatalf("baseline plan changed: %+v", base)
	}
	capped, err := linearPlan(probe, work(playback.WithVideoBitrateCeiling(playback.BaselineClientProfile(), 3000000)), playback.LinearTSOnly)
	if err != nil {
		t.Fatal(err)
	}
	if !capped.ConvertVideo || capped.VideoBitrate != 3000000 {
		t.Fatalf("capped plan: %+v", capped)
	}
}

func TestLinearCopiesCompatibleCodecsAndUnknownLiveRates(t *testing.T) {
	for _, codec := range []string{"h264", "hevc", "mpeg2video"} {
		p := playback.BaselineClientProfile()
		p.Video = []playback.ClientVideoCodec{{Codec: codec, MaxFrameRate: 60, MaxBitrateBPS: 400000000, BitDepths: []int{8}, DynamicRanges: []string{"sdr"}, Evidence: playback.EvidenceBuiltin}}
		p.Audio = []playback.ClientAudioCodec{{Codec: "ac3", MaxChannels: 6, Evidence: playback.EvidenceBuiltin}}
		p.AudioOutput.MaxChannels = 6
		for i := range p.Transports {
			if p.Transports[i].Transport == playback.TransportHLSTS {
				p.Transports[i].Video = []string{codec}
				p.Transports[i].Audio = []string{"ac3"}
			}
		}
		w := playback.LinearWork{ClientProfile: p, TranscodingEnabled: false}
		w.Selection.Quality.Mode = "original"
		w.Selection.Quality.AllowLossy = true
		raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"` + codec + `","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"0/0","field_order":"progressive"},{"codec_type":"audio","codec_name":"ac3","channels":6,"sample_rate":"48000"}]}`)
		plan, e := linearPlan(raw, w, playback.LinearTSOnly)
		if e != nil || plan.ConvertVideo || plan.ConvertAudio {
			t.Fatalf("%s: %+v %v", codec, plan, e)
		}
	}
}

func TestLinearAudioConversionKeepsCopiedHDR(t *testing.T) {
	p := playback.BaselineClientProfile()
	p.Video = append(p.Video, playback.ClientVideoCodec{Codec: "hevc", BitDepths: []int{10}, DynamicRanges: []string{"hdr10"}, MaxWidth: 3840, MaxHeight: 2160, MaxFrameRate: 60, Evidence: playback.EvidenceBuiltin})
	for i := range p.Transports {
		if p.Transports[i].Transport == playback.TransportHLSTS {
			p.Transports[i].Video = append(p.Transports[i].Video, "hevc")
		}
	}
	// The device decodes HDR10 and its screen shows it: a decoder alone would
	// leave an SDR screen with a picture it cannot present.
	p.Display.DynamicRanges = append(p.Display.DynamicRanges, "hdr10")
	w := playback.LinearWork{ClientProfile: p, TranscodingEnabled: true}
	w.Selection.Quality.AllowLossy = true
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","pix_fmt":"yuv420p10le","color_transfer":"smpte2084","width":1920,"height":1080,"avg_frame_rate":"30/1","field_order":"progressive"},{"codec_type":"audio","codec_name":"ac3","channels":6,"sample_rate":"48000"}]}`)
	plan, e := linearPlan(raw, w, playback.LinearTSOnly)
	if e != nil || plan.ConvertVideo || !plan.ConvertAudio || plan.HDRToSDR {
		t.Fatalf("%+v %v", plan, e)
	}
}

func TestLinearUsesSharedHEVCAndSurroundTargets(t *testing.T) {
	p := playback.BaselineClientProfile()
	p.Video = append(p.Video, playback.ClientVideoCodec{Codec: "hevc", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr", "hdr10"}, MaxWidth: 3840, MaxHeight: 2160})
	p.Display.DynamicRanges = []string{"sdr", "hdr10"}
	p.AudioOutput.MaxChannels = 6
	p.Audio = []playback.ClientAudioCodec{{Codec: "eac3", MaxChannels: 6}}
	for i := range p.Transports {
		if p.Transports[i].Transport == playback.TransportHLSTS {
			p.Transports[i].Video = append(p.Transports[i].Video, "hevc")
			p.Transports[i].Audio = []string{"eac3"}
		}
	}
	height := 1080
	w := playback.LinearWork{ClientProfile: p, TranscodingEnabled: true}
	w.Selection.Quality.AllowLossy = true
	w.Selection.Quality.MaxHeight = &height
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"vc1","pix_fmt":"yuv420p10le","color_transfer":"smpte2084","width":3840,"height":2160,"avg_frame_rate":"24/1"},{"codec_type":"audio","codec_name":"dts","profile":"DTS-HD MA","channels":8,"sample_rate":"96000","channel_layout":"7.1"}]}`)
	plan, err := linearPlan(raw, w, playback.LinearTSOnly)
	if err != nil || plan.Target == nil || !plan.ConvertVideo || !plan.ConvertAudio || plan.HDRToSDR || plan.Target.VideoCodec != "hevc" || plan.Target.BitDepth != 10 || plan.Target.DynamicRange != "hdr10" || plan.Target.AudioCodec != "eac3" || plan.Target.AudioChannels != 6 || plan.Target.AudioBitrateBPS != 640000 {
		t.Fatalf("%+v %v", plan.Target, err)
	}
}

// A live input may be published as fragmented MP4 when that is what keeps the
// signal: lossless sound for a device that plays FLAC only in MP4, HEVC for a
// device that takes HEVC only in MP4.
func TestLinearPlanUsesFragmentedMP4WhereItKeepsTheSignal(t *testing.T) {
	p := playback.BaselineClientProfile()
	p.AudioOutput.MaxChannels = 6
	p.Video = append(p.Video, playback.ClientVideoCodec{Codec: "hevc", BitDepths: []int{8, 10}, DynamicRanges: []string{"sdr"}, MaxWidth: 3840, MaxHeight: 2160})
	p.Audio = append(p.Audio, playback.ClientAudioCodec{Codec: "flac", MaxChannels: 8})
	p.Transports = append(p.Transports, playback.ClientTransport{Transport: playback.TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264", "hevc"}, Audio: []string{"aac", "flac"}})
	w := playback.LinearWork{ClientProfile: p, TranscodingEnabled: true}
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"hevc","profile":"Main","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"25/1"},{"codec_type":"audio","codec_name":"flac","channels":2,"sample_rate":"48000"}]}`)
	plan, err := linearPlan(raw, w, playback.LinearEither)
	if err != nil || !plan.Fragmented || plan.ConvertVideo || plan.ConvertAudio || !plan.CopyHEVC || plan.AudioCodec != "flac" {
		t.Fatalf("%+v %v", plan, err)
	}
	// The same device on a buffer that already holds MPEG-TS is planned for MPEG-TS.
	if plan, err = linearPlan(raw, w, playback.LinearTSOnly); err == nil && plan.Fragmented {
		t.Fatalf("%+v", plan)
	}
	// An ordinary H.264/AAC input stays MPEG-TS.
	raw = []byte(`{"streams":[{"codec_type":"video","codec_name":"h264","profile":"High","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"25/1"},{"codec_type":"audio","codec_name":"aac","profile":"LC","channels":2,"sample_rate":"48000"}]}`)
	if plan, err = linearPlan(raw, w, playback.LinearEither); err != nil || plan.Fragmented {
		t.Fatalf("%+v %v", plan, err)
	}
}

func TestLinearPlanCarriesCopiedTrueHDCodec(t *testing.T) {
	profile := playback.BaselineClientProfile()
	profile.Audio = append(profile.Audio, playback.ClientAudioCodec{Codec: "truehd", MaxChannels: 2})
	profile.Transports = append(profile.Transports, playback.ClientTransport{Transport: playback.TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264"}, Audio: []string{"truehd"}})
	work := playback.LinearWork{ClientProfile: profile, TranscodingEnabled: false}
	work.Selection.Quality.Mode = "original"
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"h264","profile":"High","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"25/1","field_order":"progressive"},{"codec_type":"audio","codec_name":"truehd","channels":2,"sample_rate":"48000"}]}`)
	plan, err := linearPlan(raw, work, playback.LinearFragmentedOnly)
	if err != nil || !plan.Fragmented || !plan.Audio || plan.ConvertVideo || plan.ConvertAudio || plan.AudioCodec != "truehd" || plan.CopyAAC {
		t.Fatal("selected TrueHD copy lost its mux codec", plan, err)
	}
}

// With Automatic a channel plays in mpv when the native player would need its
// picture converted and mpv takes it as it is; the viewer's choice and the
// buffer's pinned engine win otherwise.
func TestLinearEngineChoiceFollowsThePlayerSetting(t *testing.T) {
	native := playback.BaselineClientProfile()
	mpv := playback.BaselineClientProfile()
	mpv.Engine, mpv.LiveChannels = playback.EngineMPV, true
	mpv.Video = append(mpv.Video, playback.ClientVideoCodec{Codec: "mpeg2video", BitDepths: []int{8}, Interlaced: true, MaxWidth: 1920, MaxHeight: 1080})
	mpv.Audio = append(mpv.Audio, playback.ClientAudioCodec{Codec: "ac3", MaxChannels: 6})
	for i := range mpv.Transports {
		if mpv.Transports[i].Transport == playback.TransportHLSTS {
			mpv.Transports[i].Video = append(mpv.Transports[i].Video, "mpeg2video")
			mpv.Transports[i].Audio = append(mpv.Transports[i].Audio, "ac3")
		}
	}
	raw := []byte(`{"streams":[{"codec_type":"video","codec_name":"mpeg2video","profile":"Main","pix_fmt":"yuv420p","width":1920,"height":1080,"avg_frame_rate":"25/1","field_order":"tt"},{"codec_type":"audio","codec_name":"ac3","channels":6,"sample_rate":"48000"}]}`)
	w := playback.LinearWork{ClientProfile: native, MPVProfile: &mpv, EngineSetting: playback.EngineSettingAutomatic, TranscodingEnabled: true}
	w.Selection.Quality.AllowLossy = true
	plan, engine, err := linearEnginePlan(raw, w, playback.LinearEither, "")
	if err != nil || engine != playback.EngineMPV || plan.ConvertVideo || plan.ConvertAudio {
		t.Fatalf("automatic: %s %+v %v", engine, plan, err)
	}
	if _, engine, _ = linearEnginePlan(raw, w, playback.LinearEither, playback.EngineNative); engine != playback.EngineNative {
		t.Fatal("a pinned engine was changed")
	}
	w.EngineSetting = playback.EngineSettingNative
	if plan, engine, err = linearEnginePlan(raw, w, playback.LinearEither, ""); err != nil || engine != playback.EngineNative || !plan.ConvertVideo {
		t.Fatalf("native: %s %+v %v", engine, plan, err)
	}
	// Without a second engine that plays channels, the native engine plays them.
	w.EngineSetting, w.MPVProfile = playback.EngineSettingAutomatic, nil
	if _, engine, err = linearEnginePlan(raw, w, playback.LinearEither, ""); err != nil || engine != playback.EngineNative {
		t.Fatalf("no mpv: %s %v", engine, err)
	}
}

// The packaging fact comes from the same original probe and selected action as
// the audio map, never from a possible conversion target or later probe.
func TestLinearPlanQualifiesCopiedAACForFragmentedMP4(t *testing.T) {
	for _, tc := range []struct {
		name, codec                       string
		channels                          int
		packaging                         playback.LinearPackaging
		fragmented, copyAAC, convertAudio bool
	}{
		{"AAC copied to fragmented", "aac", 2, playback.LinearFragmentedOnly, true, true, false},
		{"AAC copied to TS", "aac", 2, playback.LinearTSOnly, false, false, false},
		{"AAC ordinary transport choice", "aac", 2, playback.LinearEither, false, false, false},
		{"FLAC copied to fragmented", "flac", 2, playback.LinearFragmentedOnly, true, false, false},
		{"AAC converted to fragmented", "aac", 6, playback.LinearFragmentedOnly, true, false, true},
		{"non-AAC converted to AAC", "ac3", 6, playback.LinearFragmentedOnly, true, false, true},
		{"no audio", "", 0, playback.LinearFragmentedOnly, true, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			profile := playback.BaselineClientProfile()
			profile.AudioOutput.MaxChannels = 2
			profile.Audio = []playback.ClientAudioCodec{
				{Codec: "aac", MaxChannels: 2, Evidence: playback.EvidenceBuiltin},
				{Codec: "flac", MaxChannels: 2, Evidence: playback.EvidenceBuiltin},
			}
			profile.Transports = append(profile.Transports, playback.ClientTransport{Transport: playback.TransportHLSFMP4, Containers: []string{"fmp4"}, Video: []string{"h264"}, Audio: []string{"aac", "flac"}})
			work := playback.LinearWork{ClientProfile: profile, TranscodingEnabled: true}
			work.Selection.Quality.AllowLossy = true
			streams := []map[string]any{{"codec_type": "video", "codec_name": "h264", "profile": "High", "pix_fmt": "yuv420p", "width": 1920, "height": 1080, "avg_frame_rate": "25/1", "field_order": "progressive"}}
			if tc.codec != "" {
				audio := map[string]any{"codec_type": "audio", "codec_name": tc.codec, "channels": tc.channels, "sample_rate": "48000"}
				if tc.codec == "aac" {
					audio["profile"] = "LC"
				}
				streams = append(streams, audio)
			}
			raw, err := json.Marshal(map[string]any{"streams": streams})
			if err != nil {
				t.Fatal(err)
			}
			plan, engine, err := linearEnginePlan(raw, work, tc.packaging, playback.EngineNative)
			if err != nil {
				t.Fatal(err)
			}
			if engine != playback.EngineNative || plan.Fragmented != tc.fragmented || plan.CopyAAC != tc.copyAAC || plan.ConvertAudio != tc.convertAudio || plan.Audio != (tc.codec != "") || plan.ConvertVideo {
				t.Fatalf("engine=%s plan=%+v", engine, plan)
			}
		})
	}
}
