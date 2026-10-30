package decoder

import "testing"

func profile7CopyTarget() ConversionTarget {
	return ConversionTarget{VideoCodec: "hevc", BitDepth: 10, DynamicRange: "dolby_vision", SourceRange: "dolby_vision", DolbyVisionProfile: 7, DolbyVisionCompatibility: 6, CopyDolbyVision: true, DolbyVisionConversion: DolbyVisionProfile7To81, Packaging: "fmp4_hls"}
}

func TestDolbyVisionCopyRewriteRequiresQualifiedBundle(t *testing.T) {
	target := profile7CopyTarget()
	for _, facts := range []*ToolchainFacts{nil, {}, {Identity: "name-only", BitstreamFilters: map[string]bool{"dovi_profile": true}}, {Identity: "stock", DolbyVisionProfile7To81: true, BitstreamFilters: map[string]bool{"dovi_rpu": true}}, {DolbyVisionProfile7To81: true, BitstreamFilters: map[string]bool{"dovi_profile": true}}} {
		if filter, err := DolbyVisionCopyBitstreamFilter(target, facts); err == nil || filter != "" {
			t.Fatalf("unqualified fact admitted: %+v %s %v", facts, filter, err)
		}
	}
	facts := &ToolchainFacts{Identity: "unit-qualified-cohort", DolbyVisionProfile7To81: true, BitstreamFilters: map[string]bool{"dovi_profile": true}}
	filter, err := DolbyVisionCopyBitstreamFilter(target, facts)
	if err != nil || filter != "dovi_profile=mode=2" {
		t.Fatal(filter, err)
	}
	profile, compat := target.DeliveredDolbyVision()
	if profile != 8 || compat != 1 || target.DolbyVisionProfile != 7 || target.DolbyVisionCompatibility != 6 {
		t.Fatal(profile, compat, target)
	}
	target.DolbyVisionConversion = ""
	if filter, err = DolbyVisionCopyBitstreamFilter(target, nil); filter != "" || err != nil {
		t.Fatal(filter, err)
	}
}

func TestDolbyVisionCopyRewriteTargetRejectsOtherOperations(t *testing.T) {
	for name, edit := range map[string]func(*ConversionTarget){
		"unknown":        func(t *ConversionTarget) { t.DolbyVisionConversion = "mode2" },
		"h264":           func(t *ConversionTarget) { t.VideoCodec = "h264" },
		"8bit":           func(t *ConversionTarget) { t.BitDepth = 8 },
		"sdr":            func(t *ConversionTarget) { t.DynamicRange = "sdr" },
		"not-copy":       func(t *ConversionTarget) { t.CopyDolbyVision = false },
		"source8":        func(t *ConversionTarget) { t.DolbyVisionProfile = 8 },
		"unknown-compat": func(t *ConversionTarget) { t.DolbyVisionCompatibility = 0 },
		"mpegts":         func(t *ConversionTarget) { t.Packaging = "mpegts_hls" },
		"tone-map":       func(t *ConversionTarget) { t.ToneMap = true },
	} {
		t.Run(name, func(t *testing.T) {
			target := profile7CopyTarget()
			edit(&target)
			if _, err := target.Normalized(); err == nil {
				t.Fatal("invalid rewrite target admitted", target)
			}
			if profile, _ := target.DeliveredDolbyVision(); profile != 0 {
				t.Fatal("invalid target signalled", profile)
			}
		})
	}
}
