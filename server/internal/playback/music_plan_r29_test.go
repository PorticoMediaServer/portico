package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"testing"
)

func TestMusicRateFamilyR29(t *testing.T) {
	cap := AudioDecodeCap{MaxSampleRate: 96000}
	for _, tc := range []struct{ source, want int }{{176400, 88200}, {192000, 96000}, {352800, 88200}, {384000, 96000}, {44100, 44100}} {
		if got := convertedRate(cap, tc.source); got != tc.want {
			t.Fatal(tc, got)
		}
	}
	if got := convertedRate(AudioDecodeCap{SampleRates: []int{96000}}, 44100); got != 0 {
		t.Fatal("upconversion", got)
	}
	q := qualityladder.Music()
	if len(q) != 4 || q[1].ID != "cd-quality" || q[2].AudioKbps != 320 || q[3].AudioKbps != 96 {
		t.Fatal(q)
	}
	g, peak := 12.0, 0.5
	if got := normalizationGain(&AudioGain{TrackDB: &g, TrackPeak: &peak}, "track", true); got != 0 {
		t.Fatal(got)
	}
	if got := normalizationGain(&AudioGain{TrackDB: &g, TrackPeak: &peak}, "track", false); got < 6 || got > 6.03 {
		t.Fatal(got)
	}
}

func TestMusicFLACDepthAndOwnerMusicCapR29(t *testing.T) {
	if got := flacDepthAtMost(32, 24); got != 24 {
		t.Fatal("discarded usable source depth", got)
	}
	if got := flacDepthAtMost(24, 8); got != 0 {
		t.Fatal("exceeded decoder depth", got)
	}
	if got := flacDepthAtMost(16, 32); got != 16 {
		t.Fatal("upward conversion", got)
	}
	for _, q := range []string{"original", "cd-quality", "320", "96"} {
		if got := (DeliveryConfiguration{MaxMusicQuality: q}).Normalized().MaxMusicQuality; got != q {
			t.Fatal(q, got)
		}
	}
	for _, q := range []string{"256", "192", "128", "auto", ""} {
		if got := (DeliveryConfiguration{MaxMusicQuality: q}).Normalized().MaxMusicQuality; got != "original" {
			t.Fatal(q, got)
		}
	}
}
