package catalog

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func TestProgressSelectionEpochUsesActualBrowseStateBoundaries(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	item := c.Movie(lib, "/a/a.mkv", "Alpha", 2000)
	c.Drain()
	s := New(c.DB)
	profile := "p"
	v := Viewer{Profile: profile, Fence: "f", Libraries: []string{"a"}}
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,0,0,0,'play')`, item.ID)
	epoch := func() int64 {
		var n int64
		if err := c.DB.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	states := func() []int {
		var out []int
		for _, state := range []string{"unplayed", "in-progress", "played"} {
			page, err := s.BrowseEntities(v, BrowseRequest{Library: "a", Pivot: "movies", Query: &BrowseNode{Field: "playState", Operator: "equals", Value: state}, Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, len(page.Entries))
		}
		return out
	}
	for _, tc := range []struct {
		name, sql string
		want      []int
		bumps     bool
	}{
		{"start", `UPDATE progress SET position=1 WHERE profile_id='p'`, []int{0, 1, 0}, true},
		{"same-state-checkpoint", `UPDATE progress SET position=50000 WHERE profile_id='p'`, []int{0, 1, 0}, false},
		{"near-end-is-still-Browse-in-progress", `UPDATE progress SET position=999999999 WHERE profile_id='p'`, []int{0, 1, 0}, false},
		{"zero-resume", `UPDATE progress SET position=0 WHERE profile_id='p'`, []int{1, 0, 0}, true},
		{"completed-column-is-not-watched-intent", `UPDATE progress SET completed=1 WHERE profile_id='p'`, []int{1, 0, 0}, false},
		{"unit-and-unrelated-playback-identity", `UPDATE progress SET unit=1,playback_id='another' WHERE profile_id='p'`, []int{1, 0, 0}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := epoch()
			c.Exec(tc.sql)
			after := epoch()
			if (after > before) != tc.bumps {
				t.Fatalf("epoch %d->%d", before, after)
			}
			if got := states(); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("actual Browse state %v want %v", got, tc.want)
			}
		})
	}
	c.Exec(`INSERT INTO personal_watched_intents(profile_id,item_id,watched,authored_at) VALUES('p',?,1,'2026-10-28T12:00:00Z')`, item.ID)
	before := epoch()
	c.Exec(`UPDATE progress SET position=1000 WHERE profile_id='p'`)
	// The existing recommender progress producer independently queues its
	// positive/zero boundary. The narrowed filter guard adds zero increments.
	if epoch() != before+1 || !reflect.DeepEqual(states(), []int{0, 0, 1}) {
		t.Fatal("played boundary added a filter increment", before, epoch(), states())
	}
	before = epoch()
	c.Exec(`UPDATE progress SET position=2000 WHERE profile_id='p'`)
	if epoch() != before || !reflect.DeepEqual(states(), []int{0, 0, 1}) {
		t.Fatal("routine played position changed witness")
	}
	c.Exec(`UPDATE personal_watched_intents SET watched=0 WHERE profile_id='p'`)
	if epoch() == before || !reflect.DeepEqual(states(), []int{0, 1, 0}) {
		t.Fatal("actual played/unplayed boundary lost its own witness")
	}
}

func TestA3ContinueResumeThresholdKeepsObservationWitness(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("a", "Movies", "movie", "/a")
	item := c.Movie(lib, "/a/a.mkv", "Alpha", 2000)
	c.Drain()
	s := New(c.DB)
	var duration float64
	if err := c.DB.QueryRow(`SELECT max(x.duration) FROM catalog_asset_links l JOIN catalog_assets x ON x.id=l.asset_id WHERE l.entity_id=? AND x.available=1`, item.ID).Scan(&duration); err != nil {
		t.Fatal(err)
	}
	if duration <= 4 {
		t.Fatal("fixture lacks resumable duration")
	}
	c.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES('p',?,1,0,0,'play')`, item.ID)
	c.Exec(`INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) VALUES('p','a',?,?,'paused')`, item.ID, time.Now().UTC().Format(time.RFC3339Nano))
	count, err := s.continueCount("a", "p")
	if err != nil || count != 1 {
		t.Fatal("before exact resume boundary", count, err)
	}
	observed, err := s.progressObservation("a", "p")
	if err != nil {
		t.Fatal(err)
	}
	basis, _ := smallOrderBasis(t, s, "a", "p")
	c.Exec(`UPDATE progress SET position=? WHERE profile_id='p' AND item_id=?`, int64(duration*1000)-3000, item.ID)
	c.Exec(`UPDATE progress_activity SET updated_at=? WHERE profile_id='p' AND item_id=?`, time.Now().UTC().Format(time.RFC3339Nano), item.ID)
	after, _ := smallOrderBasis(t, s, "a", "p")
	nextObserved, err := s.progressObservation("a", "p")
	if err != nil || nextObserved <= observed || basis != after {
		t.Fatal("resume threshold lost separate observation witness", observed, nextObserved, err)
	}
	count, err = s.continueCount("a", "p")
	if err != nil || count != 0 {
		t.Fatal("cached Continue ignored exact duration-3000 boundary", count, err)
	}
}

func TestPersonalCheckpointKeepsSemanticClockAndTerminalReorders(t *testing.T) {
	s, db, v := savedFixture(t)
	item := phase34SavedFixtureItem(t, db)
	profile := identity.PersonalKey(v)
	stamp := time.Now().UTC().Truncate(time.Millisecond)
	accept := func(seq int64, position float64, at time.Time) {
		t.Helper()
		tx, err := db.Begin()
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		if err = s.AcceptPlaybackProgressEvidence(tx, v, "play", item.Public, seq, position, 100, "playing", true, 1, nil, 1, true, at, nil); err != nil {
			t.Fatal(err)
		}
		if err = tx.Commit(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`INSERT INTO progress(profile_id,item_id,position,unit,completed,playback_id) VALUES(?,?,0,0,0,'play')`, profile, item.ID); err != nil {
		t.Fatal(err)
	}
	accept(1, 40, stamp)
	before, err := s.Personal(profile, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	var epoch int64
	if err = db.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	accept(2, 41, stamp.Add(15*time.Second))
	after, err := s.Personal(profile, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	var current int64
	if err = db.QueryRow(`SELECT revision FROM catalog_selection_predicate_epoch WHERE singleton=1`).Scan(&current); err != nil {
		t.Fatal(err)
	}
	if current != epoch || after.Revision != before.Revision || after.LastPlayedAt != before.LastPlayedAt || after.ProgressSeconds != 41 {
		t.Fatalf("routine checkpoint changed semantic facts: %+v -> %+v, epoch %d->%d", before, after, epoch, current)
	}
	terminal := stamp.Add(time.Minute)
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassInteractive, func(tx *sql.Tx) error { return RecordPlaybackTerminalActivityTx(tx, v, "play", item.Public, terminal) }); err != nil {
		t.Fatal(err)
	}
	after, err = s.Personal(profile, item.Public)
	if err != nil {
		t.Fatal(err)
	}
	if after.LastPlayedAt != terminal.Format("2006-01-02T15:04:05.000Z") || after.Revision != before.Revision {
		t.Fatal("terminal activity did not retain actual Stop time without new revision", after)
	}
}
