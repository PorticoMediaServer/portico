package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"testing"
)

func TestMusicQualityR28NetworkAndProvenance(t *testing.T) {
	for _, class := range []NetworkClass{NetworkLocal, NetworkRemote, NetworkUnknown} {
		for _, transport := range []string{"wifi", "ethernet", "unknown", "satellite", "cellular", "mobile", "wwan", " CELLULAR "} {
			values := WithPreferenceSources(stubPreferences{"music.qualityHome": "original", "music.qualityAway": "320", "music.qualityMobile": "320"}, map[string]string{"music.qualityHome": "default", "music.qualityAway": "default", "music.qualityMobile": "default"})
			wantDefault := "320"
			if class == NetworkLocal && NormalizeTransportClass(transport) != "cellular" {
				wantDefault = "original"
			}
			if got := SavedMusicQualityPreference(values, class, transport); got != wantDefault {
				t.Fatal(class, transport, got, wantDefault)
			}
			for _, p := range qualityladder.Music() {
				values = WithPreferenceSources(stubPreferences{"music.qualityHome": p.ID, "music.qualityAway": p.ID, "music.qualityMobile": p.ID}, map[string]string{"music.qualityAway": "profile-device-class", "music.qualityMobile": "profile-device-class"})
				want := p.ID
				if class == NetworkLocal && NormalizeTransportClass(transport) != "cellular" {
					want = p.ID
				}
				if got := SavedMusicQualityPreference(values, class, transport); got != want {
					t.Fatal(class, transport, got, want)
				}
			}
		}
	}
	// A music request cannot widen the owner's sound ceiling or remote member cap.
	choice := V1Choice{Limit: true, MaxAudioBitrateBPS: 320000, Remote: true, AdminMaxVideoBitrateBPS: 1500000}
	cfg := DefaultDeliveryConfiguration()
	cfg.MaxAudioBitrateBPS = 96000
	cfg.MaxVideoBitrateBPS = 2000000
	target := choice.target(ResolvedDeliveryPolicy{}, cfg)
	if target.MaxAudioBitrateBPS != 96000 || target.MaxVideoBitrateBPS != 1500000 {
		t.Fatal(target)
	}
	if len(SavedQualities()) != 9 {
		t.Fatal("music expanded video vocabulary")
	}
}
