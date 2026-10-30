package operations

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/qualityladder"
	"reflect"
	"testing"
)

func TestMusicQualityR28RegistryStoredOriginalAndReplay(t *testing.T) {
	for key, want := range map[string]string{"music.qualityHome": "original", "music.qualityDownloads": "320", "music.qualityAway": "320", "music.qualityMobile": "320"} {
		f, ok := PreferenceFieldFor(key)
		if !ok || f.Default != want || len(f.AllowedValues) != len(qualityladder.MusicIDs()) || !reflect.DeepEqual(f.Scopes, []string{ScopeDeviceClass}) {
			t.Fatal(key, f)
		}
		for i, id := range qualityladder.MusicIDs() {
			if f.AllowedValues[i] != id {
				t.Fatal(key, f)
			}
		}
	}
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	change := PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "mobile", ExpectedRevision: 1, IdempotencyKey: "music-stored-original", Values: PreferencePatch{"music.qualityMobile": "original", "music.qualityAway": "96"}}
	out, err := s.ApplyPreferences(ctx, p, a, change)
	if err != nil || out.Effective.Text("music.qualityMobile") != "original" {
		t.Fatal(out, err)
	}
	replay, err := s.ApplyPreferences(ctx, p, a, change)
	if err != nil || replay.Effective.Text("music.qualityMobile") != "original" {
		t.Fatal(replay, err)
	}
	values, sources, _, err := EffectivePreferences(s.DB, p.Viewer, "mobile")
	if err != nil || values.Text("music.qualityMobile") != "original" || sources["music.qualityMobile"] != ScopeDeviceClass {
		t.Fatal(values, sources, err)
	}
	other, err := s.Preferences(ctx, p, "web", a)
	if err != nil || other.Effective.Text("music.qualityMobile") != "320" || other.Effective.Text("music.qualityAway") != "320" {
		t.Fatal(other, err)
	}
	change.ExpectedRevision = 2
	change.IdempotencyKey = "music-no-video-id"
	change.Values = PreferencePatch{"music.qualityMobile": "720p-high"}
	if _, err := s.ApplyPreferences(ctx, p, a, change); err == nil {
		t.Fatal("video ID saved as music")
	}
}

func TestMusicR32DrawnDefaultsAndDeviceLocalMatching(t *testing.T) {
	if f, ok := PreferenceFieldFor("music.smoothTransitions"); !ok || f.Default != true {
		t.Fatal("812 smooth transitions default", f)
	}
	f, ok := PreferenceFieldFor("music.dsdGainDb")
	if !ok || f.Default != -6 || !reflect.DeepEqual(f.AllowedValues, []any{0, -3, -6, -9, -12}) {
		t.Fatal("812 discrete DSD controls", f)
	}
	for _, key := range []string{"music.sampleRateMatching", "music.eq"} {
		if _, ok := PreferenceFieldFor(key); ok {
			t.Fatal("device-local engine state published as account/server preference", key)
		}
	}
}
