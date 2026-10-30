package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"testing"
)

func TestStreamingMediaConfigurationContract(t *testing.T) {
	defaults := DefaultDeliveryConfiguration()
	if defaults.ConversionMode != "" || defaults.OutputFormat != "auto" || defaults.DownloadMaxQuality != "original" {
		t.Fatal(defaults)
	}
	for _, mode := range []string{"all", "sound", "never"} {
		c := defaults
		c.ConversionMode = mode
		c.OutputFormat = "h264"
		c.DownloadMaxQuality = "720p-high"
		got := c.Normalized()
		if got.ConversionMode != mode || got.OutputFormat != "h264" || got.DownloadMaxQuality != "720p-high" {
			t.Fatal(got)
		}
	}
	bad := defaults
	bad.ConversionMode = "copy"
	bad.OutputFormat = "av1"
	bad.DownloadMaxQuality = "automatic"
	got := bad.Normalized()
	if got.ConversionMode != "" || got.OutputFormat != "auto" || got.DownloadMaxQuality != "original" {
		t.Fatal(got)
	}
}

func TestAbsentConversionModeRetainsActualOwnerSwitch(t *testing.T) {
	for _, mode := range []string{"", "unknown"} {
		for _, enabled := range []bool{false, true} {
			cfg := DefaultDeliveryConfiguration()
			cfg.ConversionMode = mode
			if got := cfg.Normalized().ConversionMode; got != "" {
				t.Fatalf("unpublished mode became a policy write: %q", got)
			}
			source := videoSource("mkv", "h264", avcDetail, SourceAudio{Index: 1, Codec: "dts", Channels: 6})
			in := DeliveryInput{Source: source, Client: chromeProfile(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: cfg, TranscodingEnabled: enabled}
			plan, err := planDelivery(in)
			if !enabled && err == nil {
				t.Fatal("absent mode bypassed disabled owner conversion", plan)
			}
			if enabled && (err != nil || plan.Strategy != DeliveryAudioConversion) {
				t.Fatal("absent mode lost the owner's enabled policy", plan, err)
			}
		}
	}
}

func TestStreamingConversionModeConstrainsRealPlanner(t *testing.T) {
	for _, mode := range []string{"all", "sound", "never"} {
		t.Run(mode, func(t *testing.T) {
			cfg := DefaultDeliveryConfiguration()
			cfg.ConversionMode = mode
			source := videoSource("mkv", "h264", avcDetail, SourceAudio{Index: 1, Codec: "dts", Channels: 6})
			in := DeliveryInput{Source: source, Client: chromeProfile(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: cfg, TranscodingEnabled: true}
			plan, e := planDelivery(in)
			if mode == "never" {
				if e == nil {
					t.Fatal("never admitted conversion", plan)
				}
				return
			}
			if e != nil || plan.Strategy != DeliveryAudioConversion {
				t.Fatal(plan, e)
			}
			source.Video.Codec = "vc1"
			plan, e = planDelivery(in)
			if mode == "sound" && e == nil {
				t.Fatal("sound mode converted picture", plan)
			}
			if mode == "all" && (e != nil || plan.VideoAction != "convert") {
				t.Fatal(plan, e)
			}
		})
	}
}

func TestStreamingLegacyAndCanonicalAuthority(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		for _, mode := range []string{"", "all", "sound", "never", "unrecognized"} {
			cfg := DefaultDeliveryConfiguration()
			cfg.ConversionMode = mode
			cfg = cfg.Normalized()
			audio, video := legacy, legacy
			switch mode {
			case "all":
				audio, video = true, true
			case "sound":
				audio, video = true, false
			case "never":
				audio, video = false, false
			}
			if cfg.AudioConversionAllowed(legacy) != audio || cfg.VideoConversionAllowed(legacy) != video {
				t.Fatalf("mode=%q legacy=%v cfg=%+v", mode, legacy, cfg)
			}
		}
	}
}

func TestStreamingSoundOnlyHandlesAudioFiles(t *testing.T) {
	source := &DeliverySource{ID: "audio", Container: "mka", DetailKnown: true, Duration: 60, Audio: []SourceAudio{{Index: 0, Codec: "truehd", Channels: 6}}}
	for _, mode := range []string{"all", "sound", "never"} {
		cfg := DefaultDeliveryConfiguration()
		cfg.ConversionMode = mode
		plan, err := planDelivery(DeliveryInput{Source: source, Client: chromeProfile(), FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: cfg})
		if mode == "never" {
			if err == nil {
				t.Fatal("never converted audio", plan)
			}
			continue
		}
		if err != nil || plan.Strategy != DeliveryAudioConversion || plan.VideoAction != "none" || plan.AudioAction != "convert" {
			t.Fatalf("%s: %+v %v", mode, plan, err)
		}
	}
}

func TestStreamingOutputFormatIsOnlyAConversionRestriction(t *testing.T) {
	source := videoSource("mkv", "vc1", avcDetail, SourceAudio{Index: 1, Codec: "aac", Channels: 2})
	facts := plannerToolchain()
	cfg := DefaultDeliveryConfiguration()
	cfg.ConversionMode = "all"
	in := DeliveryInput{Source: source, Client: allOutputClient(), Server: facts, FMP4Output: true, Policy: openPolicy(), Target: QualityTarget{RungID: "original", Kind: QualityAutomatic, AllowHDR: true}, Config: cfg}
	auto, err := planDelivery(in)
	if err != nil || auto.OutputVideoCodec != "hevc" {
		t.Fatalf("auto %+v %v", auto, err)
	}
	in.Config.OutputFormat = "h264"
	restricted, err := planDelivery(in)
	if err != nil || restricted.OutputVideoCodec != "h264" || restricted.ConversionTarget == nil {
		t.Fatalf("h264 %+v %v", restricted, err)
	}
	outputs := decoder.AvailableConversionOutputs(facts)
	without := outputs
	without.Video = nil
	for _, o := range outputs.Video {
		if o.Codec != "h264" {
			without.Video = append(without.Video, o)
		}
	}
	in.Outputs = &without
	if p, err := planDelivery(in); err == nil {
		t.Fatal("invented unavailable H264 encoder", p)
	}
	source.Video.Codec = "h264"
	source.Container = "mp4"
	in.Outputs = nil
	if p, err := planDelivery(in); err != nil || p.Strategy != DeliveryOriginal {
		t.Fatalf("forced playable source conversion: %+v %v", p, err)
	}
}

func TestStreamingDownloadCeilingCanonicalLadder(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	cfg.DownloadMaxQuality = "720p-low"
	for _, q := range []string{"original", "automatic", "2160p", "1080p-high", "1080p-low", "720p-high"} {
		if got := cfg.ClampDownloadQuality(q); got != "720p-low" {
			t.Fatalf("%s -> %s", q, got)
		}
	}
	for _, q := range []string{"720p-low", "480p", "240p"} {
		if got := cfg.ClampDownloadQuality(q); got != q {
			t.Fatalf("narrowed lower rung %s -> %s", q, got)
		}
	}
	cfg.DownloadMaxQuality = "automatic"
	if got := cfg.Normalized().DownloadMaxQuality; got != "original" {
		t.Fatal(got)
	}
}
