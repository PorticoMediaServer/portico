package operations

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"testing"
)

func TestApprovedPreferenceDefaultsAndExplicitNoPreference(t *testing.T) {
	s, p, auth := consoleFixture(t)
	ctx := context.Background()
	initial, err := s.Preferences(ctx, p, "tablet", auth)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(initial.Effective.List("playback.preferredAudioLanguages"), []string{"@appLanguage", "@original"}) || !reflect.DeepEqual(initial.Effective.List("playback.preferredSubtitleLanguages"), []string{"@appLanguage"}) || initial.Effective.Text("playback.subtitleMode") != "foreign" || initial.Effective.Bool("privacy.showActivityToMembers") {
		t.Fatal(initial.Effective)
	}
	change := PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "tablet", ExpectedRevision: 1, IdempotencyKey: "explicit-empty", Values: PreferencePatch{"playback.preferredAudioLanguages": []string{}, "playback.preferredSubtitleLanguages": []string{}, "privacy.showActivityToMembers": true}}
	saved, err := s.ApplyPreferences(ctx, p, auth, change)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.Effective.List("playback.preferredAudioLanguages")) != 0 || len(saved.Effective.List("playback.preferredSubtitleLanguages")) != 0 || saved.EffectiveSource["playback.preferredAudioLanguages"] != ScopeProfileServer || !saved.Effective.Bool("privacy.showActivityToMembers") {
		t.Fatal(saved)
	}
	// A fresh store reads actual rows, including the explicit empty-list marker.
	read, err := New(s.DB).Preferences(ctx, p, "tablet", auth)
	if err != nil || !reflect.DeepEqual(read.Effective, saved.Effective) {
		t.Fatal(read, err)
	}
	var markers, items, graphs, events int
	_ = s.DB.QueryRow(`SELECT count(*) FROM preference_values WHERE kind='languageList'`).Scan(&markers)
	_ = s.DB.QueryRow(`SELECT count(*) FROM preference_list_items`).Scan(&items)
	_ = s.DB.QueryRow(`SELECT count(*) FROM console_documents WHERE scope LIKE 'profile:%' OR scope LIKE 'device:%'`).Scan(&graphs)
	_ = s.DB.QueryRow(`SELECT count(*) FROM api_events WHERE type='settings.changed'`).Scan(&events)
	if markers != 2 || items != 0 || graphs != 0 || events != 1 {
		t.Fatal(markers, items, graphs, events)
	}
	// Replay neither increments the revision nor appends another refresh event.
	if _, err = s.ApplyPreferences(ctx, p, auth, change); err != nil {
		t.Fatal(err)
	}
	_ = s.DB.QueryRow(`SELECT count(*) FROM api_events WHERE type='settings.changed'`).Scan(&events)
	if events != 1 {
		t.Fatal(events)
	}
	reset, err := s.ApplyPreferences(ctx, p, auth, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "tablet", ExpectedRevision: 2, IdempotencyKey: "reset", Values: PreferencePatch{"playback.preferredAudioLanguages": nil}})
	if err != nil || reset.EffectiveSource["playback.preferredAudioLanguages"] != "default" || len(reset.Effective.List("playback.preferredAudioLanguages")) != 2 || len(reset.Effective.List("playback.preferredSubtitleLanguages")) != 0 {
		t.Fatal(reset, err)
	}
}

func TestPreferencesFenceAuthorityProfileDeviceAndTablet(t *testing.T) {
	s, p, auth := consoleFixture(t)
	p.DeviceID = "phone-one"
	ctx := context.Background()
	saved, err := s.ApplyPreferences(ctx, p, auth, PreferenceChange{Scope: ScopeDevice, DeviceClass: "mobile", ExpectedRevision: 1, IdempotencyKey: "device-choice", Values: PreferencePatch{"appearance.motion": "reduce", "playback.useSystemCaptionStyle": true}})
	if err != nil || !saved.DeviceBound || len(saved.Documents) != 3 || saved.EffectiveSource["appearance.motion"] != ScopeDevice {
		t.Fatal(saved, err)
	}
	for _, which := range []string{"device", "authority", "account", "profile"} {
		other := p
		switch which {
		case "device":
			other.DeviceID = "phone-two"
		case "authority":
			other.Authority = "hosted"
		case "account":
			other.AccountID = "another"
		case "profile":
			other.ProfileID = "another"
		}
		read, e := s.Preferences(ctx, other, "mobile", auth)
		if e != nil || read.Effective.Text("appearance.motion") != "follow" || read.Effective.Bool("playback.useSystemCaptionStyle") {
			t.Fatal(which, read, e)
		}
	}
	if _, err = s.ApplyPreferences(ctx, p, auth, PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "tablet", ExpectedRevision: 1, IdempotencyKey: "tablet", Values: PreferencePatch{"appearance.cardSizePercent": 125}}); err != nil {
		t.Fatal(err)
	}
	phone, err := s.Preferences(ctx, p, "mobile", auth)
	if err != nil || phone.Effective.Int("appearance.cardSizePercent") != 100 {
		t.Fatal(phone, err)
	}
	p.DeviceID = ""
	if _, err = s.ApplyPreferences(ctx, p, auth, PreferenceChange{Scope: ScopeDevice, DeviceClass: "mobile", ExpectedRevision: 1, IdempotencyKey: "unbound", Values: PreferencePatch{"appearance.motion": "reduce"}}); err == nil {
		t.Fatal("a device-less credential selected device preferences")
	}
	denied := errors.New("revoked")
	if _, err = s.ApplyPreferences(ctx, p, func(context.Context, *sql.Tx, string) error { return denied }, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "mobile", ExpectedRevision: 1, IdempotencyKey: "revoked", Values: PreferencePatch{"privacy.pauseWatchHistory": true}}); !errors.Is(err, denied) {
		t.Fatal(err)
	}
	read, err := s.Preferences(ctx, p, "mobile", auth)
	if err != nil || read.Effective.Bool("privacy.pauseWatchHistory") {
		t.Fatal(read, err)
	}
}
