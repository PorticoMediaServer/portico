package playbackv1

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func observationLease(t *testing.T, db *sql.DB, id string) int64 {
	t.Helper()
	var lease int64
	if err := db.QueryRow(`SELECT lease_expires_ms FROM playback_v1_sessions WHERE id=?`, id).Scan(&lease); err != nil {
		t.Fatal(err)
	}
	return lease
}

func TestObservationReplaysCoalesceLeaseWritesAndStillRenew(t *testing.T) {
	db := pausedTestDB(t)
	clock := &observationClock{at: time.UnixMilli(1_750_000_000_000)}
	s := &Service{DB: db, Now: clock.now, LeaseDuration: 120 * time.Second}
	c := pausedCaller()
	insertPausedSession(t, db, "replayed", "vod", "playing", clock.now().UnixMilli(), 0)
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=?,last_seq=9 WHERE id='replayed'`, clock.now().Add(s.lease()).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	r := newReporter(t, s, c)
	grant, err := s.ClaimObserver(context.Background(), c, "replayed", ObserverClaim{Generation: 1})
	if err != nil || grant.EveryMs != ObservationEvery.Milliseconds() {
		t.Fatalf("claim: %+v error %v", grant, err)
	}
	r.epochs["replayed"] = grant.Epoch
	if err := r.report("replayed", Observation{Generation: 1, State: "playing", PositionMs: 7000}); err != nil {
		t.Fatal(err)
	}
	initialLease := observationLease(t, db, "replayed")
	// A duplicate has the last accepted sequence; an older generation has a
	// fresh epoch sequence but neither may replace the accepted evidence.
	repeat := Observation{Epoch: grant.Epoch, Seq: 1, Generation: 1, State: "playing", PositionMs: 1000}
	before := dbwork.ChangingCommits()
	for i := 0; i < 100; i++ {
		ob := repeat
		if i%2 == 1 {
			ob.Seq, ob.Generation, ob.State, ob.PositionMs = int64(i+2), 0, "paused", 2000
		}
		if ack, err := s.Observe(context.Background(), c, "replayed", ob); err != nil || ack.LeaseUntil.UnixMilli() != initialLease {
			t.Fatalf("replay ack %+v error %v", ack, err)
		}
		r.applyDue("replayed")
	}
	stats := s.ObservationStats()
	if dbwork.ChangingCommits() != before || observationLease(t, db, "replayed") != initialLease || stats.Applied != 1 || stats.Duplicate != 50 || stats.StaleGeneration != 50 || stats.Renewed != 0 {
		t.Fatalf("duplicate/stale observations performed unnecessary writes: %+v", stats)
	}
	// Renewal starts when observationRenew remains of the durable lease.
	clock.add(s.lease() - observationRenew)
	if _, err := s.Observe(context.Background(), c, "replayed", repeat); err != nil {
		t.Fatal(err)
	}
	r.applyDue("replayed")
	if got := observationLease(t, db, "replayed"); got != clock.now().Add(s.lease()).UnixMilli() || got <= initialLease {
		t.Fatal("replay did not renew its lease before expiry")
	}
	before = dbwork.ChangingCommits()
	for i := 0; i < 10; i++ {
		if ack, err := s.Observe(context.Background(), c, "replayed", repeat); err != nil || ack.LeaseUntil.UnixMilli() != observationLease(t, db, "replayed") {
			t.Fatalf("renewed replay ack %+v error %v", ack, err)
		}
		r.applyDue("replayed")
	}
	if stats = s.ObservationStats(); dbwork.ChangingCommits() != before || stats.Applied != 1 || stats.Renewed != 1 || stats.Duplicate != 61 {
		t.Fatalf("renewed lease was written again by immediate replays: %+v", stats)
	}
	var state string
	var seq, position int64
	if err := db.QueryRow(`SELECT state,last_seq,position_ms FROM playback_v1_sessions WHERE id='replayed'`).Scan(&state, &seq, &position); err != nil {
		t.Fatal(err)
	}
	if state != "playing" || seq != 10 || position != 7000 {
		t.Fatal("duplicate or stale observations changed playback evidence")
	}
	r.seq["replayed"] = 101
	if err := r.report("replayed", Observation{Generation: 1, State: "paused", PositionMs: 8000}); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT state,last_seq,position_ms FROM playback_v1_sessions WHERE id='replayed'`).Scan(&state, &seq, &position); err != nil {
		t.Fatal(err)
	}
	if state != "paused" || seq != 11 || position != 8000 {
		t.Fatal("fresh state and progress observation was not applied by the batch")
	}
}

func TestObservationReplayRenewalKeepsShortLeaseHeadroom(t *testing.T) {
	db := pausedTestDB(t)
	clock := &observationClock{at: time.UnixMilli(1_750_000_000_000)}
	s := &Service{DB: db, Now: clock.now, LeaseDuration: 200 * time.Millisecond}
	c := pausedCaller()
	insertPausedSession(t, db, "short_replay", "vod", "playing", clock.now().UnixMilli(), 0)
	if _, err := db.Exec(`UPDATE playback_v1_sessions SET lease_expires_ms=? WHERE id='short_replay'`, clock.now().Add(s.lease()).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	r := newReporter(t, s, c)
	if err := r.report("short_replay", Observation{Generation: 1, State: "playing"}); err != nil {
		t.Fatal(err)
	}
	repeat := Observation{Epoch: r.epochs["short_replay"], Seq: 1, Generation: 1, State: "playing"}
	for i := 0; i < 5; i++ {
		clock.add(60 * time.Millisecond)
		if _, err := s.Observe(context.Background(), c, "short_replay", repeat); err != nil {
			t.Fatal(err)
		}
		r.applyDue("short_replay")
		if observationLease(t, db, "short_replay") != clock.now().Add(s.lease()).UnixMilli() {
			t.Fatal("short lease was not renewed with sufficient headroom")
		}
	}
	if stats := s.ObservationStats(); stats.Applied != 1 || stats.Renewed != 5 || stats.Duplicate != 5 {
		t.Fatalf("short-lease replays rewrote evidence or missed renewal: %+v", stats)
	}
}
