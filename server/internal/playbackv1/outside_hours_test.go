package playbackv1

import (
	"context"
	"strings"
	"testing"
	"time"
)

// An account whose allowed hours close while it watches has every running
// session ended with the reason, and the session.updated event says why. Other
// accounts keep playing, and with no schedule check wired nothing ends.
func TestEndOutsideHoursEndsTheAccountsSessionsWithTheReason(t *testing.T) {
	ctx := context.Background()
	db := pausedTestDB(t)
	now := time.UnixMilli(1_750_000_000_000)
	s := &Service{DB: db, Now: func() time.Time { return now }}
	insertPausedSession(t, db, "oh_movie", "vod", "playing", now.UnixMilli(), 0)
	insertPausedSession(t, db, "oh_live", "live", "paused", now.UnixMilli(), 0)
	insertPausedSession(t, db, "oh_other", "vod", "playing", now.UnixMilli(), 0)
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET account_id='member' WHERE id='oh_other'`); err != nil {
		t.Fatal(err)
	}
	if n := s.EndOutsideHours(ctx); n != 0 {
		t.Fatalf("ended %d with no schedule check, want 0", n)
	}
	s.OutsideHours = func(_ context.Context, account string) bool { return account == "owner" }
	if n := s.EndOutsideHours(ctx); n != 2 {
		t.Fatalf("ended %d sessions, want 2", n)
	}
	for id, want := range map[string]string{"oh_movie": EndReasonOutsideHours, "oh_live": EndReasonOutsideHours, "oh_other": ""} {
		var reason string
		if err := db.QueryRow(`SELECT end_reason FROM playback_v1_sessions WHERE id=?`, id).Scan(&reason); err != nil {
			t.Fatal(err)
		}
		if reason != want {
			t.Fatalf("%s: end reason %q, want %q", id, reason, want)
		}
	}
	var data string
	if err := db.QueryRow(`SELECT data FROM api_events WHERE audience='device:dev1' AND type='session.updated' AND resource_id='oh_movie' ORDER BY id DESC LIMIT 1`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, `"reason":"outside_allowed_hours"`) {
		t.Fatalf("session.updated does not carry the reason: %s", data)
	}
}
