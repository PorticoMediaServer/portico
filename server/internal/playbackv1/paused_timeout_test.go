package playbackv1

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// pausedTestDB is a real installed schema for paused-session tests.
func pausedTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func pausedCaller() Caller {
	return Caller{
		Principal:        identity.Principal{Viewer: identity.Viewer{Authority: "local", AccountID: "owner", ProfileID: "primary"}},
		DeviceID:         "dev1",
		PlaybackProtocol: PlaybackProtocol,
	}
}

// insertPausedSession writes one session row directly: the create paths need a
// catalog item and playback machinery, but paused_since_ms maintenance and the
// sweeper only need the row.
func insertPausedSession(t *testing.T, db *sql.DB, id, kind, state string, nowMs, pausedSinceMs int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO playback_v1_sessions(id,device_id,account_id,profile_id,authority,start_key,start_digest,kind,role,state,request,revision,generation,lease_expires_ms,created_ms,updated_ms,paused_since_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, "dev1", "owner", "primary", "local", "key-"+id, "digest", kind, "local", state, "{}", 1, 1, nowMs+2*60*60*1000, nowMs, nowMs, pausedSinceMs)
	if err != nil {
		t.Fatal(err)
	}
}

func pausedState(t *testing.T, db *sql.DB, id string) (state string, pausedSinceMs int64, revision int) {
	t.Helper()
	if err := db.QueryRow(`SELECT state,paused_since_ms,revision FROM playback_v1_sessions WHERE id=?`, id).Scan(&state, &pausedSinceMs, &revision); err != nil {
		t.Fatal(err)
	}
	return state, pausedSinceMs, revision
}

// Entering paused stamps when; staying paused keeps the stamp; any other state
// clears it — through both observations and PATCH.
func TestPausedSinceSetOnEnterKeptWhilePausedClearedOnResume(t *testing.T) {
	ctx := context.Background()
	db := pausedTestDB(t)
	clock := &observationClock{at: time.UnixMilli(1_750_000_000_000)}
	now := clock.now()
	// A long lease: the test moves the clock minutes at a time and must not
	// trip lease expiry.
	s := &Service{DB: db, Now: clock.now, LeaseDuration: 2 * time.Hour}
	c := pausedCaller()
	insertPausedSession(t, db, "ps_play", "vod", "playing", now.UnixMilli(), 0)
	r := newReporter(t, s, c)

	// A paused report enters paused: stamp now.
	if err := r.report("ps_play", Observation{Generation: 1, State: "paused", PositionMs: 1000}); err != nil {
		t.Fatal(err)
	}
	state, stamp, _ := pausedState(t, db, "ps_play")
	if state != "paused" || stamp != now.UnixMilli() {
		t.Fatalf("after pausing: state=%q paused_since_ms=%d, want paused and %d", state, stamp, now.UnixMilli())
	}

	// Another paused report stays paused: keep the old stamp.
	clock.add(5 * time.Minute)
	now = clock.now()
	// The simulated reporting gap exceeds observationIdle. Reclaim before
	// reporting again whether or not the worker already retired the owner.
	delete(r.epochs, "ps_play")
	r.seq["ps_play"] = 0
	if err := r.report("ps_play", Observation{Generation: 1, State: "paused", PositionMs: 1000}); err != nil {
		t.Fatal(err)
	}
	if state, stamp, _ := pausedState(t, db, "ps_play"); state != "paused" || stamp != now.Add(-5*time.Minute).UnixMilli() {
		t.Fatalf("while paused: state=%q paused_since_ms=%d, want paused and the first stamp", state, stamp)
	}

	// A playing report resumes: clear to 0.
	if err := r.report("ps_play", Observation{Generation: 1, State: "playing", PositionMs: 2000}); err != nil {
		t.Fatal(err)
	}
	if state, stamp, _ := pausedState(t, db, "ps_play"); state != "playing" || stamp != 0 {
		t.Fatalf("after resume: state=%q paused_since_ms=%d, want playing and 0", state, stamp)
	}

	// PATCH to paused stamps now too (revision is 3 after the three reports:
	// two of them changed state).
	clock.add(time.Minute)
	now = clock.now()
	if _, err := s.Patch(ctx, c, "ps_play", "3", Change{State: "paused"}); err != nil {
		t.Fatal(err)
	}
	if state, stamp, _ := pausedState(t, db, "ps_play"); state != "paused" || stamp != now.UnixMilli() {
		t.Fatalf("after patch to paused: state=%q paused_since_ms=%d, want paused and %d", state, stamp, now.UnixMilli())
	}

	// A PATCH that leaves the session paused keeps the stamp.
	clock.add(time.Minute)
	now = clock.now()
	if _, err := s.Patch(ctx, c, "ps_play", "4", Change{}); err != nil {
		t.Fatal(err)
	}
	if state, stamp, _ := pausedState(t, db, "ps_play"); state != "paused" || stamp != now.Add(-time.Minute).UnixMilli() {
		t.Fatalf("after no-op patch: state=%q paused_since_ms=%d, want paused and the kept stamp", state, stamp)
	}
}

// Only a video session paused past the limit ends: audio (track, audiobook)
// and Live/channel sessions paused just as long do not, nor do playing,
// recently-paused or never-stamped video sessions.
func TestEndPausedTooLongEndsVideoOnly(t *testing.T) {
	ctx := context.Background()
	db := pausedTestDB(t)
	now := time.UnixMilli(1_750_000_000_000)
	s := &Service{DB: db, Now: func() time.Time { return now }}
	s.PausedLimit = func(context.Context) time.Duration { return 30 * time.Minute }
	old := now.Add(-time.Hour).UnixMilli()
	insertPausedSession(t, db, "ps_movie", "vod", "paused", now.UnixMilli(), old)
	insertPausedSession(t, db, "ps_track", "audio", "paused", now.UnixMilli(), old)
	insertPausedSession(t, db, "ps_book", "audio", "paused", now.UnixMilli(), old)
	insertPausedSession(t, db, "ps_live", "live", "paused", now.UnixMilli(), old)
	insertPausedSession(t, db, "ps_channel", "channel", "paused", now.UnixMilli(), old)
	insertPausedSession(t, db, "ps_playing", "vod", "playing", now.UnixMilli(), 0)
	insertPausedSession(t, db, "ps_recent", "vod", "paused", now.UnixMilli(), now.Add(-10*time.Minute).UnixMilli())
	insertPausedSession(t, db, "ps_legacy", "vod", "paused", now.UnixMilli(), 0)

	if n := s.EndPausedTooLong(ctx); n != 1 {
		t.Fatalf("ended %d sessions, want 1", n)
	}
	var ended int64
	var reason, message string
	if err := db.QueryRow(`SELECT ended_ms,end_reason,message FROM playback_v1_sessions WHERE id='ps_movie'`).Scan(&ended, &reason, &message); err != nil {
		t.Fatal(err)
	}
	if ended == 0 || reason != EndReasonPausedTimeout || message != PausedTimeoutMessage {
		t.Fatalf("movie: ended_ms=%d reason=%q message=%q", ended, reason, message)
	}
	for _, id := range []string{"ps_track", "ps_book", "ps_live", "ps_channel", "ps_playing", "ps_recent", "ps_legacy"} {
		if err := db.QueryRow(`SELECT ended_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&ended); err != nil {
			t.Fatal(err)
		}
		if ended != 0 {
			t.Fatalf("%s ended, want it to survive", id)
		}
	}
}

// No limit (nil or 0) ends nothing, and neither does a pause still within the
// limit.
func TestEndPausedTooLongRespectsLimitAndOff(t *testing.T) {
	ctx := context.Background()
	now := time.UnixMilli(1_750_000_000_000)

	t.Run("nil", func(t *testing.T) {
		db := pausedTestDB(t)
		s := &Service{DB: db, Now: func() time.Time { return now }}
		insertPausedSession(t, db, "ps_movie", "vod", "paused", now.UnixMilli(), now.Add(-time.Hour).UnixMilli())
		if n := s.EndPausedTooLong(ctx); n != 0 {
			t.Fatalf("ended %d sessions with no limit, want 0", n)
		}
	})

	t.Run("zero", func(t *testing.T) {
		db := pausedTestDB(t)
		s := &Service{DB: db, Now: func() time.Time { return now }}
		s.PausedLimit = func(context.Context) time.Duration { return 0 }
		insertPausedSession(t, db, "ps_movie", "vod", "paused", now.UnixMilli(), now.Add(-time.Hour).UnixMilli())
		if n := s.EndPausedTooLong(ctx); n != 0 {
			t.Fatalf("ended %d sessions with limit 0, want 0", n)
		}
	})

	t.Run("within", func(t *testing.T) {
		db := pausedTestDB(t)
		s := &Service{DB: db, Now: func() time.Time { return now }}
		s.PausedLimit = func(context.Context) time.Duration { return 30 * time.Minute }
		insertPausedSession(t, db, "ps_movie", "vod", "paused", now.UnixMilli(), now.Add(-10*time.Minute).UnixMilli())
		if n := s.EndPausedTooLong(ctx); n != 0 {
			t.Fatalf("ended %d sessions paused within the limit, want 0", n)
		}
	})
}

// The viewer-facing ended response carries the pause-timeout reason and message
// the same way an administrator terminate does.
func TestPausedTimeoutEndReasonAndMessage(t *testing.T) {
	ctx := context.Background()
	db := pausedTestDB(t)
	now := time.UnixMilli(1_750_000_000_000)
	s := &Service{DB: db, Now: func() time.Time { return now }}
	s.PausedLimit = func(context.Context) time.Duration { return 30 * time.Minute }
	c := pausedCaller()
	insertPausedSession(t, db, "ps_movie", "vod", "paused", now.UnixMilli(), now.Add(-time.Hour).UnixMilli())
	insertPausedSession(t, db, "ps_admin", "vod", "paused", now.UnixMilli(), 0)

	if n := s.EndPausedTooLong(ctx); n != 1 {
		t.Fatalf("ended %d sessions, want 1", n)
	}
	v, err := s.Get(ctx, c, "ps_movie")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != "ended" || v.End == nil || v.End.Reason != "paused_timeout" || v.End.Message != "Playback stopped after a long pause." {
		t.Fatalf("ended view: %+v", v)
	}
	if err := s.Terminate(ctx, "ps_admin", "The server is restarting."); err != nil {
		t.Fatal(err)
	}
	admin, err := s.Get(ctx, c, "ps_admin")
	if err != nil {
		t.Fatal(err)
	}
	if admin.State != "ended" || admin.End == nil || admin.End.Reason != "terminated" || admin.End.Message == "" {
		t.Fatalf("admin-terminated view: %+v", admin)
	}

	// The session.updated event carries the reason like other ended sessions.
	var data string
	if err := db.QueryRow(`SELECT data FROM api_events WHERE audience='device:dev1' AND type='session.updated' AND resource_id='ps_movie' ORDER BY id DESC LIMIT 1`).Scan(&data); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(data, `"reason":"paused_timeout"`) {
		t.Fatalf("latest session.updated event does not carry the reason: %s", data)
	}
}
