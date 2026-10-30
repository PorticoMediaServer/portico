package operations

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

func TestPreferenceRegistryIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range PreferenceRegistry() {
		if seen[f.Key] {
			t.Fatalf("duplicate registry key %q", f.Key)
		}
		seen[f.Key] = true
		if len(f.Scopes) == 0 || f.Group == "" || f.LabelKey != "preferences."+f.Key {
			t.Fatalf("incomplete field %+v", f)
		}
		for _, scope := range f.Scopes {
			if scope != ScopeProfileServer && scope != ScopeDeviceClass && scope != ScopeDevice {
				t.Fatalf("unpublished scope %q on %q", scope, f.Key)
			}
		}
		// Every default must itself survive the field's own validation, or the
		// server would publish a value it would refuse on the way back in.
		if _, clamped, e := canonicalPreference(f, f.Default); e != nil || clamped {
			t.Fatalf("default is not acceptable for %q: %v clamped=%t", f.Key, e, clamped)
		}
	}
	for _, key := range []string{
		"playback.startedThresholdPercent", "playback.playedThresholdPercent", "delivery.transcode",
		"quality.home", "quality.remote", "music.crossfadeMs", "privacy.pauseWatchHistory",
		"search.rememberHistory", "region.hourCycle", "home.rowOrder",
		"appearance.cardSizePercent", "notifications.badges",
	} {
		if !seen[key] {
			t.Fatalf("plan field %q is missing from the registry", key)
		}
	}
	// The published registry must round-trip as JSON for the client contract.
	b, e := json.Marshal(PreferenceRegistryDocument{Revision: PreferenceRegistryRevision, Fields: PreferenceRegistry()})
	if e != nil || len(b) < 1000 {
		t.Fatalf("registry does not publish: %v", e)
	}
}

func TestPreferencePatchValidationAndClamping(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	// A number outside its range clamps and says so; the enum beside it is kept.
	out, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "clamp", Values: PreferencePatch{
		"playback.startedThresholdPercent": 90,
		"playback.playedThresholdPercent":  10,
		"music.audioNormalization":         "album",
	}})
	if e != nil {
		t.Fatal(e)
	}
	if out.Effective.Int("playback.startedThresholdPercent") != 25 || out.Effective.Int("playback.playedThresholdPercent") != 75 {
		t.Fatalf("not clamped: %+v", out.Effective)
	}
	if out.Effective.Text("music.audioNormalization") != "album" {
		t.Fatal("enum lost")
	}
	if len(out.ClampedFields) != 2 || out.ClampedFields[0] != "playback.playedThresholdPercent" || out.ClampedFields[1] != "playback.startedThresholdPercent" {
		t.Fatalf("clampedFields not reported: %+v", out.ClampedFields)
	}
	var fields *ValidationError
	// Unknown key.
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "unknown", Values: PreferencePatch{"playback.warpDrive": true}})
	if !errors.As(e, &fields) || len(fields.Fields) != 1 || fields.Fields[0] != "values.playback.warpDrive" {
		t.Fatalf("unknown key was not named: %v", e)
	}
	// Known key in the wrong scope.
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "wrong-scope", Values: PreferencePatch{"appearance.motion": "reduce"}})
	if !errors.As(e, &fields) || fields.Fields[0] != "values.appearance.motion" {
		t.Fatalf("scope violation was not named: %v", e)
	}
	// Value outside an enumerated domain is refused rather than quietly rewritten.
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "bad-enum", Values: PreferencePatch{"playback.defaultSpeed": 3.5}})
	if !errors.As(e, &fields) || fields.Fields[0] != "values.playback.defaultSpeed" {
		t.Fatalf("enum violation was not named: %v", e)
	}
	// Wrong type.
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "bad-type", Values: PreferencePatch{"search.rememberHistory": "yes"}})
	if !errors.As(e, &fields) || fields.Fields[0] != "values.search.rememberHistory" {
		t.Fatalf("type violation was not named: %v", e)
	}
	// A duplicate entry in a list is a client bug, not a value to deduplicate.
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "bad-list", Values: PreferencePatch{"playback.preferredAudioLanguages": []any{"en", "en"}}})
	if !errors.As(e, &fields) || fields.Fields[0] != "values.playback.preferredAudioLanguages" {
		t.Fatalf("list violation was not named: %v", e)
	}
	// None of the rejections advanced the document.
	current, e := s.Preferences(ctx, p, "web", a)
	if e != nil || current.Documents[0].Revision != 2 {
		t.Fatalf("rejected patches moved the revision: %v %+v", e, current.Documents[0])
	}
}

// A viewer has two saved default qualities, one for the owner's home networks
// and one for everywhere else. Both start at the original, take every
// published quality, and are kept per device class.
func TestSavedQualityDefaults(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	for _, key := range []string{"quality.home", "quality.remote"} {
		f, ok := PreferenceFieldFor(key)
		if !ok || f.Default != "original" || len(f.AllowedValues) != len(SavedQualities) {
			t.Fatalf("%s: %+v", key, f)
		}
	}
	for _, gone := range []string{"quality.local.mode", "quality.wifi.maxVideoBitrateMbps", "quality.cellular.allowHDR", "quality.unknown.maxVideoHeight"} {
		if _, ok := PreferenceFieldFor(gone); ok {
			t.Fatal("a per-network quality preset survived", gone)
		}
	}
	out, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "television", ExpectedRevision: 1, IdempotencyKey: "tv", Values: PreferencePatch{"quality.remote": "automatic", "quality.home": "1080p-low"}})
	if e != nil || out.Effective.Text("quality.remote") != "automatic" || out.Effective.Text("quality.home") != "1080p-low" || len(out.ClampedFields) != 0 {
		t.Fatalf("%v %+v", e, out.Effective)
	}
	// Another device class keeps its own.
	mobile, e := s.Preferences(ctx, p, "mobile", a)
	if e != nil || mobile.Effective.Text("quality.remote") != "original" {
		t.Fatalf("a television's saved quality reached a phone: %v", e)
	}
	if _, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "television", ExpectedRevision: 2, IdempotencyKey: "bad", Values: PreferencePatch{"quality.home": "900p"}}); e == nil {
		t.Fatal("an unpublished quality was saved")
	}
}

func TestPreferenceSnapshotPublishesRegistryAndDefaults(t *testing.T) {
	s, p, a := consoleFixture(t)
	out, e := s.Preferences(context.Background(), p, "web", a)
	if e != nil {
		t.Fatal(e)
	}
	if out.Registry.Revision != PreferenceRegistryRevision || len(out.Registry.Fields) != len(PreferenceRegistry()) {
		t.Fatalf("registry not published: %+v", out.Registry)
	}
	if len(out.Documents) != 2 {
		t.Fatalf("scope documents missing: %+v", out.Documents)
	}
	if len(out.Effective) != len(PreferenceRegistry()) {
		t.Fatal("effective values must cover every registry field")
	}
	if !out.Effective.Bool("search.rememberHistory") || out.Effective.Int("playback.playedThresholdPercent") != 95 || out.EffectiveSource["playback.playedThresholdPercent"] != "default" {
		t.Fatalf("defaults are wrong: %+v", out.Effective)
	}
	if list := out.Effective.List("home.rowOrder"); len(list) != 0 {
		t.Fatal("list default must be empty, not null")
	}
}

func TestEffectivePreferencesAccessorReadsWithoutHTTP(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	if _, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "accessor", Values: PreferencePatch{"playback.startedThresholdPercent": 12}}); e != nil {
		t.Fatal(e)
	}
	values, source, _, e := EffectivePreferences(s.DB, p.Viewer, "")
	if e != nil || values.Int("playback.startedThresholdPercent") != 12 || source["playback.startedThresholdPercent"] != ScopeProfileServer {
		t.Fatalf("accessor did not read the stored value: %v %+v", e, values)
	}
}

// A profile document saved while region.timeZone was still a preference keeps
// reading: the retired key is left out, the keys beside it are kept, and the
// next save writes the document without it.
func TestStoredPreferenceForARetiredKeyIsIgnored(t *testing.T) {
	s, p, a := consoleFixture(t)
	address, _ := preferenceAddressFor(p.Viewer, ScopeProfileServer, "web", "")
	if _, err := s.DB.Exec(`INSERT INTO preference_scopes VALUES(?,?,?,?,?,4,0)`, address.args()...); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"region.timeZone", "music.defaultSpeed"} {
		if _, err := s.DB.Exec(`INSERT INTO preference_values VALUES(?,?,?,?,?,?,'string',NULL,NULL,'retired')`, append(address.args(), key)...); err != nil {
			t.Fatal(err)
		}
	}
	out, err := s.Preferences(context.Background(), p, "web", a)
	if err != nil || out.Documents[0].Revision != 4 || len(out.Documents[0].Values) != 0 {
		t.Fatal(out, err)
	}
	for _, key := range []string{"region.timeZone", "music.defaultSpeed", "appearance.reduceMotion"} {
		if _, ok := out.Effective[key]; ok {
			t.Fatal("retired field published", key)
		}
		if _, err = s.ApplyPreferences(context.Background(), p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 4, IdempotencyKey: key, Values: PreferencePatch{key: "retired"}}); err == nil {
			t.Fatal("retired patch accepted", key)
		}
	}
	if _, err = s.ApplyPreferences(context.Background(), p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 4, IdempotencyKey: "after-retirement", Values: PreferencePatch{"region.hourCycle": "h12"}}); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.DB.QueryRow(`SELECT count(*) FROM preference_values WHERE key IN('region.timeZone','music.defaultSpeed')`).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}

func TestPreferenceConsumersAndSemanticDomains(t *testing.T) {
	for _, f := range PreferenceRegistry() {
		if f.Consumer == "" {
			t.Fatalf("unconsumed preference %s", f.Key)
		}
		if f.Type == "integer" && (f.Step == nil || *f.Step != 1) {
			t.Fatalf("integer step missing: %s", f.Key)
		}
	}
	for _, key := range []string{"music.shuffleDefault", "music.repeatDefault", "navigation.sidebarCollapsed", "region.timeZone"} {
		if _, ok := PreferenceFieldFor(key); ok {
			t.Fatal("removed key survived", key)
		}
	}
	cases := []struct {
		key       string
		good, bad any
	}{
		{"region.locale", "fr-CA", "not_a_real_locale"},
		{"playback.preferredAudioLanguages", []string{"en", "fr-CA"}, []string{"en", "EN"}},
		{"quality.remote", "720p-high", "data-saver"},
	}
	for _, c := range cases {
		f, _ := PreferenceFieldFor(c.key)
		if _, _, err := canonicalPreference(f, c.good); err != nil {
			t.Fatal(c.key, err)
		}
		if _, _, err := canonicalPreference(f, c.bad); err == nil {
			t.Fatal("bad value accepted", c.key)
		}
	}
	if f, _ := PreferenceFieldFor("region.locale"); f.Default != "auto" {
		t.Fatal("region.locale")
	}
	for _, f := range PreferenceRegistry() {
		if f.Type == "timeZone" {
			t.Fatal("a viewer time zone preference is published", f.Key)
		}
	}
	values := PreferenceValues{"music.gapless": false, "music.crossfadeMs": int64(6000), "music.audioNormalization": "album"}
	effects := values.AudioEffects()
	if effects.Gapless || effects.CrossfadeMs != 6000 || effects.Normalization != "album" {
		t.Fatal(effects)
	}
}

func TestQualityDefaultsR25StoredOriginalProvenance(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	out, err := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeDeviceClass, DeviceClass: "mobile", ExpectedRevision: 1, IdempotencyKey: "explicit-original", Values: PreferencePatch{"quality.remote": "original"}})
	if err != nil || out.Effective.Text("quality.remote") != "original" {
		t.Fatal(out, err)
	}
	tx, err := s.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	values, sources, _, err := EffectivePreferences(tx, p.Viewer, "mobile")
	if err != nil || values.Text("quality.home") != "original" || sources["quality.home"] != "default" || sources["quality.remote"] != ScopeDeviceClass {
		t.Fatal(values, sources, err)
	}
}

func TestQualityDefaultsR25ServerCapRegistry(t *testing.T) {
	defaults := DefaultSettings()
	if defaults.RemoteBitrateLimitKbps != 0 || defaults.UploadCapacityKbps != 0 {
		t.Fatal(defaults)
	}
	found := 0
	for _, field := range Registry() {
		if field.ID == "remoteBitrateLimitKbps" || field.ID == "uploadCapacityKbps" {
			found++
			if field.Default != 0 {
				t.Fatal(field)
			}
		}
	}
	if found != 2 {
		t.Fatal(found)
	}
}
