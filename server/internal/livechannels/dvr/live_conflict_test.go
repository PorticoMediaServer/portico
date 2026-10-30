package dvr

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// Justin's decision (15 Oct): when a recording needs a tuner someone is
// watching, the owner's setting decides. By default the viewer keeps watching:
// the recording waits (the owner is told), starts late if a tuner frees up in
// its window, and fails with its own reason if none does. "Stop watching"
// keeps the countdown and the tuner goes to the recording.
func TestRecordingAgainstALiveViewerFollowsTheOwnersSetting(t *testing.T) {
	owner := livechannels.Owner{Authority: "local", AccountID: "account", ProfileID: "profile"}
	setup := func(t *testing.T, policy string) (*Store, string) {
		t.Helper()
		s, source, p, a := sharingStore(t)
		for _, q := range []string{
			`INSERT INTO accounts(id,username,password_hash,profile_id) VALUES('admin','admin',x'00','admin-profile')`,
			`INSERT INTO direct_memberships(account_id,role) SELECT 'admin','owner' WHERE NOT EXISTS(SELECT 1 FROM direct_memberships WHERE role='owner')`,
			`UPDATE dvr_defaults SET live_tuner_conflict='` + policy + `'`,
			// Someone is watching another channel on the source's only tuner.
			`INSERT INTO live_allocations VALUES('viewer','` + source.ID + `','other','viewer-session','live','local','account','viewer','token',1,'active',` + strconv.FormatInt(time.Now().Add(time.Hour).UnixMilli(), 10) + `,1,0)`,
		} {
			if _, e := s.db.Exec(q); e != nil {
				t.Fatalf("%s: %v", q, e)
			}
		}
		r, e := s.Schedule(context.Background(), a, owner, ScheduleInput{RequestID: strings.Repeat("c", 48), Occurrence: Occurrence{source.ID, p.ChannelID, source.Generation, p.ID}})
		if e != nil {
			t.Fatal(e)
		}
		return s, r.ID
	}
	recording := func(s *Store, id string) (state, reason string) {
		t.Helper()
		if e := s.db.QueryRow(`SELECT state,reason FROM dvr_recordings WHERE id=?`, id).Scan(&state, &reason); e != nil {
			t.Fatal(e)
		}
		return state, reason
	}
	notice := func(s *Store) string {
		var title string
		_ = s.db.QueryRow(`SELECT title FROM notification_records WHERE source='dvr' AND category='dvr.conflict'`).Scan(&title)
		return title
	}
	countdowns := func(s *Store) (events int) {
		_ = s.db.QueryRow(`SELECT count(*) FROM api_events WHERE type='channel.reservation-required'`).Scan(&events)
		return events
	}

	t.Run("keep watching: the recording waits, then starts late", func(t *testing.T) {
		s, id := setup(t, livechannels.KeepWatching)
		ctx := context.Background()
		if c, _, e := s.claimNext(ctx); e != nil || c != nil {
			t.Fatalf("claim against a live viewer: %v %v", c, e)
		}
		if state, reason := recording(s, id); state != "scheduled" || reason != "tuner-in-use-live" {
			t.Fatalf("waiting recording: %s %s", state, reason)
		}
		if title := notice(s); title != "Recording is waiting for a tuner" {
			t.Fatalf("owner notice %q", title)
		}
		if countdowns(s) != 0 {
			t.Fatal("the viewer was counted down")
		}
		var preempt int64
		_ = s.db.QueryRow(`SELECT preempt_at_ms FROM live_allocations WHERE id='viewer'`).Scan(&preempt)
		if preempt != 0 {
			t.Fatal("the viewer was given a deadline")
		}
		// The viewer stops; the recording takes the tuner on its next look.
		if _, e := s.db.Exec(`UPDATE live_allocations SET state='released' WHERE id='viewer'`); e != nil {
			t.Fatal(e)
		}
		now := time.Now().Add(5 * time.Second)
		s.now = func() time.Time { return now }
		if c, _, e := s.claimNext(ctx); e != nil || c == nil {
			t.Fatalf("claim once the tuner is free: %v %v", c, e)
		}
		if title := notice(s); title != "Recording started late" {
			t.Fatalf("owner notice after a late start %q", title)
		}
	})

	t.Run("keep watching: no tuner frees up in the window", func(t *testing.T) {
		s, id := setup(t, livechannels.KeepWatching)
		ctx := context.Background()
		if c, _, e := s.claimNext(ctx); e != nil || c != nil {
			t.Fatalf("claim against a live viewer: %v %v", c, e)
		}
		later := time.Now().Add(3 * time.Hour)
		s.now = func() time.Time { return later }
		if _, e := s.db.Exec(`UPDATE live_allocations SET lease_until_ms=? WHERE id='viewer'`, later.Add(time.Hour).UnixMilli()); e != nil {
			t.Fatal(e)
		}
		if c, _, e := s.claimNext(ctx); e != nil || c != nil {
			t.Fatalf("claim after the window: %v %v", c, e)
		}
		if state, reason := recording(s, id); state != "failed" || reason != "tuner-in-use-live" {
			t.Fatalf("missed recording: %s %s", state, reason)
		}
		if title := notice(s); title != "Recording did not start" {
			t.Fatalf("owner notice %q", title)
		}
	})

	t.Run("stop watching: the viewer is counted down", func(t *testing.T) {
		s, id := setup(t, livechannels.StopWatching)
		if c, _, e := s.claimNext(context.Background()); e != nil || c != nil {
			t.Fatalf("claim against a live viewer: %v %v", c, e)
		}
		if _, reason := recording(s, id); reason != "tuner-preemption-countdown" {
			t.Fatalf("reason %s", reason)
		}
		if countdowns(s) != 1 {
			t.Fatal("the viewer was not told")
		}
	})
}
