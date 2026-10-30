package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// The per-platform bars in VIDEO.md §5, planned against each platform's own
// capability document. These are the decision-table rows for the Apple TV 4K
// and the Android TV / Shield / Fire TV Cube boxes.

func barDV(profile, compatibility int, tag string) assets.StreamDetail {
	d := hdr10Detail
	d.DynamicRange = assets.RangeDolbyVision
	d.DolbyVisionProfile = profile
	d.DolbyVisionCompatibility = compatibility
	d.DolbyVisionLevel = 6
	d.CodecTag = tag
	return d
}

func barPlan(t *testing.T, client ClientProfile, source *DeliverySource, mutate func(*DeliveryInput)) DeliveryPlan {
	t.Helper()
	policy := openPolicy()
	in := DeliveryInput{Source: source, Client: client, Server: matrixToolchain(), Policy: policy, Target: autoTarget(policy, source.Video.Height), Config: DefaultDeliveryConfiguration(), TranscodingEnabled: true, FMP4Output: true}
	if mutate != nil {
		mutate(&in)
	}
	p, err := planDelivery(in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Apple TV 4K: Dolby Vision 5, 8.1 and 8.4 arrive as Dolby Vision, profile 7
// is rewritten to 8.1, and TrueHD and DTS-HD Master Audio arrive as 7.1 LPCM.
func TestAppleTVBarDeliversItsDolbyVisionAndLosslessSurround(t *testing.T) {
	apple := matrixProfiles(t)["appletv_dv"]
	for i := range apple.Video {
		if apple.Video[i].Codec == "hevc" {
			// The 2026 model reports both 8.1 and 8.4 modes.
			apple.Video[i].DolbyVisionProfile8Compatibility = []int{1, 4}
		}
	}
	eac3 := SourceAudio{Codec: "eac3", Channels: 8, StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "eac3_joc"}, ObjectAudio: "atmos"}}
	for _, c := range []struct{ profile, compatibility int }{{5, 0}, {8, 1}, {8, 4}} {
		p := barPlan(t, apple, videoSource("mp4", "hevc", barDV(c.profile, c.compatibility, "dvh1"), eac3), nil)
		info := PlaybackInfoForPlan(p, nil)
		if p.Strategy != DeliveryOriginal || info == nil || info.Delivered.Video.DolbyVisionProfile != c.profile {
			t.Fatalf("Dolby Vision %d.%d was not delivered as Dolby Vision: %+v %+v", c.profile, c.compatibility, p, info)
		}
		if c.profile == 8 && info.Delivered.Video.DolbyVisionCompatibility != c.compatibility {
			t.Fatalf("Dolby Vision 8.%d kept the wrong compatibility id: %+v", c.compatibility, info.Delivered.Video)
		}
	}

	// A profile 7 disc is converted to 8.1 for this device.
	p7 := observedProfile7Source("FEL")
	qualified := packetQualifiedUnitFacts()
	rewritten := barPlan(t, apple, p7, func(in *DeliveryInput) { in.Server = qualified })
	if rewritten.Strategy != DeliveryCopyRemux || rewritten.ConversionTarget == nil || rewritten.ConversionTarget.DolbyVisionConversion != decoder.DolbyVisionProfile7To81 {
		t.Fatalf("profile 7 was not rewritten to 8.1: %+v", rewritten)
	}
	if info := PlaybackInfoForPlan(rewritten, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 8 || info.Delivered.Video.DolbyVisionCompatibility != 1 {
		t.Fatalf("the rewritten picture was not signalled as 8.1: %+v", info)
	}

	// Lossless surround is decoded to multichannel PCM on this device.
	truehd := SourceAudio{Codec: "truehd", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "truehd_atmos"}, ObjectAudio: "atmos"}}
	if p := barPlan(t, apple, videoSource("mkv", "h264", avcDetail, truehd), nil); p.OutputAudioCodec != "flac" || p.AudioChannels != 8 {
		t.Fatalf("TrueHD Atmos did not arrive as 7.1 LPCM: %+v", p)
	}
	dtsma := SourceAudio{Codec: "dts", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "dts_hd_ma"}}}
	if p := barPlan(t, apple, videoSource("mkv", "h264", avcDetail, dtsma), nil); p.OutputAudioCodec != "flac" || p.AudioChannels != 8 {
		t.Fatalf("DTS-HD Master Audio did not arrive as 7.1 LPCM: %+v", p)
	}
}

// Android TV / Shield / Fire TV Cube: Dolby Vision including profile 7 where
// the device declares it, HDR10+, and TrueHD Atmos and DTS:X passthrough.
func TestAndroidTVBarKeepsItsDolbyVisionAndObjectAudio(t *testing.T) {
	shield := matrixProfiles(t)["shield_avr"]
	shield.Display.DynamicRanges = append(shield.Display.DynamicRanges, "hdr10plus")
	for i := range shield.Video {
		if shield.Video[i].Codec == "hevc" {
			shield.Video[i].DolbyVisionProfiles = []int{5, 7, 8}
			shield.Video[i].DolbyVisionProfile8Compatibility = []int{1}
			shield.Video[i].DynamicRanges = append(shield.Video[i].DynamicRanges, "hdr10plus")
		}
	}
	// The Shield takes profile 7 itself, untouched.
	p7 := observedProfile7Source("FEL")
	p7.Container = "mp4"
	untouched := barPlan(t, shield, p7, nil)
	if untouched.Strategy != DeliveryOriginal || untouched.ConversionTarget != nil {
		t.Fatalf("a declared profile 7 was not played untouched: %+v", untouched)
	}
	if info := PlaybackInfoForPlan(untouched, nil); info == nil || info.Delivered.Video.DolbyVisionProfile != 7 {
		t.Fatalf("the untouched picture lost its profile: %+v", info)
	}

	// HDR10+ metadata survives on a device that declares it.
	plus := hdr10Detail
	plus.HDR10Plus = true
	if p := barPlan(t, shield, videoSource("mkv", "hevc", plus, SourceAudio{Codec: "aac", Channels: 2}), nil); p.Strategy != DeliveryOriginal {
		t.Fatalf("HDR10+ was not played as it is: %+v", p)
	} else if info := PlaybackInfoForPlan(p, nil); info == nil || info.Delivered.Video.HDR10Plus == nil || !*info.Delivered.Video.HDR10Plus {
		t.Fatalf("HDR10+ metadata was lost: %+v", info)
	}

	// Object audio goes to the receiver undecoded.
	truehd := SourceAudio{Codec: "truehd", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "truehd_atmos"}, ObjectAudio: "atmos"}}
	atmos := barPlan(t, shield, videoSource("mkv", "h264", avcDetail, truehd), nil)
	if !atmos.AudioPassthrough || atmos.OutputAudioCodec != "truehd" || atmos.AudioAction == "convert" {
		t.Fatalf("TrueHD Atmos was not passed through: %+v", atmos)
	}
	dtsx := SourceAudio{Codec: "dts", Channels: 8, Layout: "7.1", StreamDetail: assets.StreamDetail{StreamBreadth: assets.StreamBreadth{AudioVariant: "dtsx"}, ObjectAudio: "dtsx"}}
	x := barPlan(t, shield, videoSource("mkv", "h264", avcDetail, dtsx), nil)
	if !x.AudioPassthrough || x.OutputAudioCodec != "dts" {
		t.Fatalf("DTS:X was not passed through: %+v", x)
	}
}
