package operations

import (
	"context"
	"errors"
	"testing"
)

func TestAudiobookJumpPreferencesPersistIndependently(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	before, err := s.Preferences(ctx, p, "web", a)
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]int{"audiobooks.skipBackMs": 15000, "audiobooks.skipForwardMs": 30000, "audiobooks.resumeRewindMs": 0} {
		if before.Effective.Int(key) != want {
			t.Fatalf("%s default = %d", key, before.Effective.Int(key))
		}
	}
	out, err := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "audiobook-jumps", Values: PreferencePatch{"audiobooks.skipBackMs": 60000, "audiobooks.skipForwardMs": 90000, "audiobooks.resumeRewindMs": 5000}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Effective.Int("audiobooks.skipBackMs") != 60000 || out.Effective.Int("audiobooks.skipForwardMs") != 90000 || out.Effective.Int("audiobooks.resumeRewindMs") != 5000 {
		t.Fatal("audiobook choices not persisted")
	}
	if out.Effective.Int("playback.skipBackMs") != before.Effective.Int("playback.skipBackMs") || out.Effective.Int("playback.skipForwardMs") != before.Effective.Int("playback.skipForwardMs") {
		t.Fatal("audiobook choices changed video jumps")
	}
	for _, key := range []string{"audiobooks.skipBackMs", "audiobooks.skipForwardMs", "audiobooks.resumeRewindMs"} {
		_, err = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "invalid-" + key, Values: PreferencePatch{key: 7000}})
		var invalid *ValidationError
		if !errors.As(err, &invalid) {
			t.Fatalf("%s accepted unsupported choice: %v", key, err)
		}
	}
}
