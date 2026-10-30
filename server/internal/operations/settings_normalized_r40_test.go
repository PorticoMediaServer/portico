package operations

import (
	"context"
	"testing"
)

func TestNormalizedSettingsWriterReloadsCanonicalMusicAndModes(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	before, err := s.Settings(ctx, authorize)
	if err != nil {
		t.Fatal(err)
	}
	values := before.Requested
	values.MusicAwayQuality = "cd-quality"
	values.ConversionMode = "sound"
	values.OutputFormat = "h264"
	values.DownloadMaxQuality = "720p-low"
	after, err := s.ApplySettings(ctx, owner, authorize, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "normalized-music", Values: values})
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision+1 || after.Requested.MusicAwayQuality != "cd-quality" || !after.Requested.TranscodingEnabled {
		t.Fatal("normalized current revision/mode mismatch")
	}
	var music, mode, format, download string
	if err = s.DB.QueryRow(`SELECT "musicAwayQuality","conversionMode","outputFormat","downloadMaxQuality" FROM server_settings WHERE singleton=1`).Scan(&music, &mode, &format, &download); err != nil || music != "cd-quality" || mode != "sound" || format != "h264" || download != "720p-low" {
		t.Fatal("typed persistence mismatch", err)
	}
	var graphs int
	if err = s.DB.QueryRow(`SELECT count(*) FROM console_documents WHERE scope='server'`).Scan(&graphs); err != nil || graphs != 0 {
		t.Fatal("settings graph mirror exists", err)
	}
	reloaded, err := New(s.DB).Settings(ctx, authorize)
	if err != nil || reloaded.Requested.MusicAwayQuality != music || reloaded.Requested.ConversionMode != mode {
		t.Fatal("normalized reload changed source", err)
	}
	for _, bad := range []string{"", "cd", "256", "automatic"} {
		invalid := values
		invalid.MusicAwayQuality = bad
		if _, err = s.ApplySettings(ctx, owner, authorize, SettingsChange{ExpectedRevision: after.Revision, IdempotencyKey: "bad-" + bad, Values: invalid}); err == nil {
			t.Fatal("noncanonical music quality accepted")
		}
	}
}

func TestNormalizedSettingsPreserveLegacyGateUntilCanonicalModeIsStored(t *testing.T) {
	s, owner, authorize := consoleFixture(t)
	ctx := context.Background()
	if _, err := s.DB.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=0 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	before, err := s.Settings(ctx, authorize)
	if err != nil || before.Requested.ConversionMode != "never" || before.Requested.TranscodingEnabled {
		t.Fatal("legacy disabled owner reopened", before, err)
	}
	values := before.Requested
	values.MusicAwayQuality = "96"
	after, err := s.ApplySettings(ctx, owner, authorize, SettingsChange{ExpectedRevision: before.Revision, IdempotencyKey: "preserve-owner-conversion", Values: values})
	if err != nil || after.Requested.ConversionMode != "never" || after.Requested.TranscodingEnabled {
		t.Fatal("unrelated save reopened conversion", after, err)
	}
	if _, err = s.DB.Exec(`UPDATE playback_owner_policy SET transcoding_enabled=1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	after, err = s.Settings(ctx, authorize)
	if err != nil || after.Requested.ConversionMode != "never" || after.Requested.TranscodingEnabled {
		t.Fatal("legacy boolean replaced canonical mode", after, err)
	}
}
