package operations

import (
	"context"
	"errors"
	"testing"
)

// Audiobooks may be saved at up to three times speed; music and video stop at two.
func TestAudiobookSpeedGoesToThreeTimes(t *testing.T) {
	s, p, a := consoleFixture(t)
	ctx := context.Background()
	out, e := s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 1, IdempotencyKey: "book-speed", Values: PreferencePatch{"audiobooks.defaultSpeed": 3}})
	if e != nil {
		t.Fatal(e)
	}
	if out.Effective.Float("audiobooks.defaultSpeed") != 3 {
		t.Fatalf("audiobook speed not kept: %v", out.Effective.Float("audiobooks.defaultSpeed"))
	}
	for _, key := range []string{"music.defaultSpeed", "playback.defaultSpeed"} {
		var fields *ValidationError
		_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "fast-" + key, Values: PreferencePatch{key: 3}})
		if !errors.As(e, &fields) {
			t.Fatalf("%s accepted three times speed: %v", key, e)
		}
	}
	_, e = s.ApplyPreferences(ctx, p, a, PreferenceChange{Scope: ScopeProfileServer, DeviceClass: "web", ExpectedRevision: 2, IdempotencyKey: "too-fast", Values: PreferencePatch{"audiobooks.defaultSpeed": 4}})
	var fields *ValidationError
	if !errors.As(e, &fields) {
		t.Fatalf("four times speed accepted: %v", e)
	}
}
