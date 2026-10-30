package mbaccounting_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mbaccounting"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func accountingDB(t *testing.T) *sql.DB {
	t.Helper()
	db, e := persistence.OpenFresh(filepath.Join(t.TempDir(), "accounting.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	return db
}
func writer(t *testing.T, db *sql.DB, fn func(*sql.Tx) error) {
	t.Helper()
	if e := dbwork.WithWriteTx(context.Background(), db, dbwork.ClassBackgroundMedia, fn); e != nil {
		t.Fatal(e)
	}
}
func exec(t *testing.T, tx *sql.Tx, q string, args ...any) {
	t.Helper()
	if _, e := tx.Exec(q, args...); e != nil {
		t.Fatal(e)
	}
}

// This fixture uses the production baseline accounting triggers. Its final
// callback models the domain's one release-reference decrement; actual pin and
// publication-authority integration is verified by the domain tests separately.
func unpinFixture(t *testing.T, db *sql.DB, tracks int) mbaccounting.Change {
	t.Helper()
	writer(t, db, func(tx *sql.Tx) error {
		for _, r := range []string{"a", "b"} {
			exec(t, tx, `INSERT INTO mb_recording_evidence(revision_id,provider_id,digest,title,artist,disambiguation,observed_at,payload) VALUES(?,?,?,'T','A','','now','{}')`, r, r, strings.Repeat(r, 64))
			exec(t, tx, `UPDATE mb_recording_evidence SET sealed=1 WHERE revision_id=?`, r)
		}
		exec(t, tx, `INSERT INTO mb_release_evidence(revision_id,provider_id,digest,title,artist,release_group_id,group_title,primary_type,date,country,barcode,disambiguation,observed_at,payload) VALUES('release','release',?,'T','A','','','','','','','','now','{}')`, strings.Repeat("c", 64))
		// Accounting fixtures need only track PKs and recording memberships. Actual
		// release media uses the same production FK and ready-build definitions.
		exec(t, tx, `INSERT INTO mb_release_media(release_revision,position,title,format) VALUES('release',1,'','CD')`)
		for n := 0; n < tracks; n++ {
			rec := "a"
			if n%2 != 0 {
				rec = "b"
			}
			exec(t, tx, `INSERT INTO mb_release_tracks(release_revision,track_id,medium_position,position,number,title,artist,recording_revision) VALUES('release',?,1,?,'','T','A',?)`, fmt.Sprintf("%05d", n), n+1, rec)
		}
		return nil
	})
	// Seed acceptance through the same coordinator, without forging a phase or
	// assigning counters. The callback models this domain-independent fixture's
	// single release-reference insertion; domain tests cover actual link rows.
	pin := mbaccounting.Change{Owner: mbaccounting.Owner{Kind: "publication", Operation: "fixture-pin", Generation: 1}, ID: "fixture-pin", Link: "album", Entity: 1, Digest: "fixture-pin", NewRelease: "release"}
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, pin); return e })
	prepare(t, db, pin)
	writer(t, db, func(tx *sql.Tx) error {
		return mbaccounting.FlipTx(context.Background(), tx, pin, func() error {
			_, e := tx.Exec(`UPDATE mb_release_accounting SET direct_refs=1 WHERE revision_id='release'`)
			return e
		}, func() error { return nil })
	})
	fold(t, db)
	return mbaccounting.Change{Owner: mbaccounting.Owner{Kind: "entity-mutation", Operation: "mutation", Generation: 1}, ID: "transition", Link: "album", Entity: 1, Digest: "pin-digest", OldRelease: "release"}
}
func state(t *testing.T, db *sql.DB) mbaccounting.State {
	t.Helper()
	var s mbaccounting.State
	writer(t, db, func(tx *sql.Tx) error { var e error; s, e = mbaccounting.ReadTx(context.Background(), tx); return e })
	return s
}
func refs(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	var n int64
	if e := db.QueryRow(`SELECT sum(release_refs) FROM mb_recording_accounting`).Scan(&n); e != nil {
		t.Fatal(e)
	}
	return n
}
func prepare(t *testing.T, db *sql.DB, c mbaccounting.Change) {
	t.Helper()
	for n := 0; n < 40; n++ {
		var done bool
		writer(t, db, func(tx *sql.Tx) error {
			var e error
			done, e = mbaccounting.PrepareTx(context.Background(), tx, c)
			return e
		})
		if done {
			return
		}
	}
	t.Fatal("preparation did not converge")
}
func fold(t *testing.T, db *sql.DB) {
	t.Helper()
	for n := 0; n < 40; n++ {
		var done bool
		writer(t, db, func(tx *sql.Tx) error { var e error; done, e = mbaccounting.FoldTx(context.Background(), tx); return e })
		if done {
			return
		}
	}
	t.Fatal("fold did not converge")
}
func TestAccountingPrivateCopyFlipAndFoldHaveExactVisibleCounts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 37)
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, c); return e })
	// More than one raw window, including duplicate recordings. Every private
	// checkpoint leaves the old authoritative values visible.
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.PrepareTx(context.Background(), tx, c); return e })
	if n := refs(t, db); n != 37 {
		t.Fatal("private copy changed visible facts", n)
	}
	prepare(t, db, c)
	if n := refs(t, db); n != 37 {
		t.Fatal("ready copy changed visible facts", n)
	}
	writer(t, db, func(tx *sql.Tx) error {
		return mbaccounting.FlipTx(context.Background(), tx, c, func() error {
			_, e := tx.Exec(`UPDATE mb_release_accounting SET direct_refs=0 WHERE revision_id='release'`)
			return e
		}, func() error { return nil })
	})
	if n := refs(t, db); n != 0 {
		t.Fatal("published overlay incomplete", n)
	}
	fold(t, db)
	if n := refs(t, db); n != 0 || state(t, db).Phase != "idle" {
		t.Fatal("fold changed published facts", n)
	}
	var records int64
	if e := db.QueryRow(`SELECT recordings FROM mb_evidence_totals WHERE singleton=1`).Scan(&records); e != nil || records != 2 {
		t.Fatal(records, e)
	}
}
func TestAccountingAbortKeepsOldFactsAndReleasesOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 37)
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, c); return e })
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.PrepareTx(context.Background(), tx, c); return e })
	writer(t, db, func(tx *sql.Tx) error {
		if e := mbaccounting.IdleTx(context.Background(), tx); !errors.Is(e, mbaccounting.ErrBusy) {
			t.Fatal("unrelated producer was not paused", e)
		}
		return mbaccounting.AbortTx(context.Background(), tx, c.Owner)
	})
	fold(t, db)
	if n := refs(t, db); n != 37 || state(t, db).Phase != "idle" {
		t.Fatal("abort lost old authority", n)
	}
}

func TestAccountingAbandonedPrivateOwnerExpiresWithoutChangingPinFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 37)
	start := time.Unix(1000, 0)
	writer(t, db, func(tx *sql.Tx) error {
		_, e := mbaccounting.BeginTx(mbaccounting.WithTime(context.Background(), start), tx, c)
		return e
	})
	writer(t, db, func(tx *sql.Tx) error {
		_, e := mbaccounting.PrepareTx(mbaccounting.WithTime(context.Background(), start.Add(10*time.Second)), tx, c)
		return e
	})
	writer(t, db, func(tx *sql.Tx) error {
		worked, e := mbaccounting.MaintainTx(mbaccounting.WithTime(context.Background(), start.Add(101*time.Second)), tx, nil)
		if !worked && e == nil {
			t.Fatal("expired cancelled owner retained global pause")
		}
		return e
	})
	fold(t, db)
	if n := refs(t, db); n != 37 {
		t.Fatal("expiry changed old authority", n)
	}
	writer(t, db, func(tx *sql.Tx) error { return mbaccounting.IdleTx(context.Background(), tx) })
}

func TestAccountingSlowRealProgressRenewsPrivateInactivityLease(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 37)
	now := time.Unix(1000, 0)
	writer(t, db, func(tx *sql.Tx) error {
		_, e := mbaccounting.BeginTx(mbaccounting.WithTime(context.Background(), now), tx, c)
		return e
	})
	ready := false
	for n := 0; n < 20 && !ready; n++ {
		now = now.Add(80 * time.Second)
		writer(t, db, func(tx *sql.Tx) error {
			ctx := mbaccounting.WithTime(context.Background(), now)
			var e error
			ready, e = mbaccounting.PrepareTx(ctx, tx, c)
			if e != nil {
				return e
			}
			worked, e := mbaccounting.MaintainTx(ctx, tx, nil)
			if worked && e == nil {
				t.Fatal("progressing owner expired")
			}
			return e
		})
	}
	if !ready {
		t.Fatal("slow copy did not finish")
	}
	if n := refs(t, db); n != 37 {
		t.Fatal("slow private copy changed public counts", n)
	}
	if got := state(t, db).ExpiresMS; got != now.Add(90*time.Second).UnixMilli() {
		t.Fatal("lease was not progress-relative", got)
	}
}

func TestAccountingRestartAtEveryDurablePhaseReleasesProducers(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, phase := range []string{"prepare", "ready", "fold"} {
		t.Run(phase, func(t *testing.T) {
			db := accountingDB(t)
			c := unpinFixture(t, db, 37)
			writer(t, db, func(tx *sql.Tx) error {
				for n := 0; n < 33; n++ {
					id := fmt.Sprintf("unique-%03d", n)
					exec(t, tx, `INSERT INTO mb_recording_evidence(revision_id,provider_id,digest,title,artist,disambiguation,observed_at,payload) VALUES(?,?,?,'T','A','','now','{}')`, id, id, fmt.Sprintf("%064x", n+100))
					exec(t, tx, `UPDATE mb_recording_evidence SET sealed=1 WHERE revision_id=?`, id)
					exec(t, tx, `INSERT INTO mb_release_tracks(release_revision,track_id,medium_position,position,number,title,artist,recording_revision) VALUES('release',?,1,?,'','T','A',?)`, id, n+100, id)
				}
				_, e := mbaccounting.BeginTx(context.Background(), tx, c)
				return e
			})
			writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.PrepareTx(context.Background(), tx, c); return e })
			if phase != "prepare" {
				prepare(t, db, c)
			}
			if phase == "fold" {
				writer(t, db, func(tx *sql.Tx) error {
					return mbaccounting.FlipTx(context.Background(), tx, c, func() error {
						_, e := tx.Exec(`UPDATE mb_release_accounting SET direct_refs=0 WHERE revision_id='release'`)
						return e
					}, func() error { return nil })
				})
				writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.FoldTx(context.Background(), tx); return e })
				var next int
				if e := db.QueryRow(`SELECT count(*) FROM mb_recording_accounting_next`).Scan(&next); e != nil || next == 0 {
					t.Fatal("fixture did not interrupt a partial fold", next, e)
				}
			}
			var seq int
			var name, path string
			if e := db.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); e != nil {
				t.Fatal(e)
			}
			if e := db.Close(); e != nil {
				t.Fatal(e)
			}
			reopened, e := persistence.OpenFresh(path)
			if e != nil {
				t.Fatal(e)
			}
			defer reopened.Close()
			for turn := 0; turn < 20 && state(t, reopened).Phase != "idle"; turn++ {
				writer(t, reopened, func(tx *sql.Tx) error {
					_, e := mbaccounting.MaintainTx(context.Background(), tx, func(mbaccounting.Owner) (bool, error) { return false, nil })
					return e
				})
			}
			if state(t, reopened).Phase != "idle" {
				t.Fatal("restart stranded accounting owner")
			}
			want := int64(70)
			if phase == "fold" {
				want = 0
			}
			if got := refs(t, reopened); got != want {
				t.Fatal("restart changed accepted facts", got, want)
			}
			writer(t, reopened, func(tx *sql.Tx) error { return mbaccounting.IdleTx(context.Background(), tx) })
		})
	}
}

func TestAccountingPrivateOwnerDoesNotBlockUserOrPlaybackWriters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 37)
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, c); return e })
	// These go through the real gate as ordinary user/playback writers while the
	// accounting singleton is deliberately left held between separate turns.
	for _, class := range []dbwork.Class{dbwork.ClassInteractive, dbwork.ClassEstablishedPlayback} {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		e := dbwork.WithWriteTx(ctx, db, class, func(tx *sql.Tx) error {
			_, e := tx.ExecContext(ctx, `INSERT INTO configuration(key,value) VALUES('accounting-user-test',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`, class.String())
			return e
		})
		cancel()
		if e != nil {
			t.Fatal(class, e)
		}
		if state(t, db).Phase != "prepare" {
			t.Fatal("ordinary writer changed accounting custody")
		}
	}
}

func TestAccountingPhaseGuardAllowsOnlyTheApprovedEdges(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	db := accountingDB(t)
	c := unpinFixture(t, db, 0)
	edges := map[string]map[string]bool{
		"idle":    {"prepare": true},
		"prepare": {"ready": true, "fold": true},
		"ready":   {"flip": true, "fold": true},
		"flip":    {"fold": true},
		"fold":    {"idle": true},
	}
	checked := map[string]bool{}
	check := func(tx *sql.Tx, from string) {
		t.Helper()
		var got string
		if e := tx.QueryRow(`SELECT phase FROM mb_accounting_state WHERE singleton=1`).Scan(&got); e != nil || got != from {
			t.Fatal(from, got, e)
		}
		checked[from] = true
		for _, to := range []string{"idle", "prepare", "ready", "flip", "fold"} {
			if to == from || edges[from][to] {
				continue
			}
			if _, e := tx.Exec(`UPDATE mb_accounting_state SET phase=? WHERE singleton=1`, to); e == nil {
				t.Fatalf("illegal phase %s -> %s passed", from, to)
			}
			if e := tx.QueryRow(`SELECT phase FROM mb_accounting_state WHERE singleton=1`).Scan(&got); e != nil || got != from {
				t.Fatal("refused transition changed phase", from, to, got, e)
			}
		}
		if _, e := tx.Exec(`UPDATE mb_accounting_state SET phase=? WHERE singleton=1`, from); e != nil {
			t.Fatal("same-phase progress refused", from, e)
		}
	}
	writer(t, db, func(tx *sql.Tx) error {
		check(tx, "idle")
		_, e := mbaccounting.BeginTx(context.Background(), tx, c)
		return e
	})
	writer(t, db, func(tx *sql.Tx) error { check(tx, "prepare"); return nil })
	prepare(t, db, c) // prepare -> ready
	writer(t, db, func(tx *sql.Tx) error {
		check(tx, "ready")
		return mbaccounting.FlipTx(context.Background(), tx, c, func() error {
			check(tx, "flip") // ready -> flip; FlipTx subsequently takes flip -> fold.
			_, e := tx.Exec(`UPDATE mb_release_accounting SET direct_refs=0 WHERE revision_id='release'`)
			return e
		}, func() error { return nil })
	})
	writer(t, db, func(tx *sql.Tx) error { check(tx, "fold"); return nil })
	fold(t, db) // fold -> idle
	// The two legal abort edges must also work through the real coordinator.
	pin := c
	pin.ID, pin.Digest, pin.OldRelease, pin.NewRelease = "abort-prepare", "abort-prepare", "", "release"
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, pin); return e })
	writer(t, db, func(tx *sql.Tx) error { return mbaccounting.AbortTx(context.Background(), tx, pin.Owner) }) // prepare -> fold
	fold(t, db)
	pin.ID, pin.Digest = "abort-ready", "abort-ready"
	writer(t, db, func(tx *sql.Tx) error { _, e := mbaccounting.BeginTx(context.Background(), tx, pin); return e })
	prepare(t, db, pin)
	writer(t, db, func(tx *sql.Tx) error { return mbaccounting.AbortTx(context.Background(), tx, pin.Owner) }) // ready -> fold
	fold(t, db)
	if len(checked) != 5 || state(t, db).Phase != "idle" {
		t.Fatal("phase coverage or final state", checked)
	}
}
