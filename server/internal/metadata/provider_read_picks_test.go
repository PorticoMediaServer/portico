package metadata

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/localonly"
	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
	"github.com/PorticoMediaServer/portico/server/internal/worker"
)

func readPickContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func readPickCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func readPickRelease(t *testing.T, db *sql.DB, title string) string {
	t.Helper()
	value := mbLaneReleaseFixture()
	value.Title = title
	stage, err := stageMBRelease(metadataprovider.ReleaseLookup{RequestedID: mbLaneRelease, Release: value})
	if err != nil {
		t.Fatal(err)
	}
	var revision string
	if err = dbwork.WithWriteTx(context.Background(), db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var e error
		revision, e = saveMBReleaseEvidence(context.Background(), tx, stage, "2026-10-27T12:00:00Z")
		return e
	}); err != nil {
		t.Fatal(err)
	}
	return revision
}

func readPickPin(t *testing.T, db *sql.DB, album int64, release, status string) {
	t.Helper()
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: album, NewRelease: release}, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'now')`, album, release, mbLaneRelease, mbLaneRelease)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(`INSERT INTO mb_album_reconciliations(album_id,release_revision,membership_revision,status) SELECT album_id,?,membership_revision,? FROM mb_album_heads WHERE album_id=?`, release, status, album); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// Even a settled catalogue must finish its read while another writer owns the
// gate. This proves gate scope, not a bounded read walk (the latter is deferred).
func TestProviderReadPicksMBSettledDoesNotAcquireWriter(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	release := readPickRelease(t, db, "Settled")
	readPickPin(t, db, names["album"].ID, release, "complete")
	for i := 1; i < 256; i++ {
		album := c.Album(names["artist"], fmt.Sprintf("Settled %03d", i), 2020)
		readPickPin(t, db, album.ID, release, "complete")
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE status='complete'`); n != 256 {
		t.Fatal(n)
	}
	ctx := readPickContext(t)
	hold, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	before := backgroundGateAcquisitions()
	ctx, trace := dbwork.TraceQueryPlans(ctx) // Full SQL text only; no EXPLAIN.
	done, err := New(db, "").mbReconcileAlbums(ctx)
	if err != nil || done {
		t.Fatal(done, err)
	}
	if n := backgroundGateAcquisitions() - before; n != 0 {
		t.Fatalf("settled read acquired %d writers", n)
	}
	if got := trace(); len(got) != 1 || !strings.Contains(got[0], "ORDER BY c.album_id LIMIT 1") {
		t.Fatalf("settled read statements: %v", got)
	}
	last := readPickCount(t, db, `SELECT max(album_id) FROM mb_album_reconciliations`)
	if _, err := hold.Tx().ExecContext(ctx, `UPDATE mb_album_reconciliations SET status='pending' WHERE album_id=?`, last); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, dueTrace := dbwork.TraceQueryPlans(ctx)
	done, err = New(db, "").mbReconcileAlbums(ctx)
	if err != nil || !done {
		t.Fatal("due album behind settled prefix", done, err)
	}
	if n := backgroundGateAcquisitions() - before; n != 1 {
		t.Fatalf("due pass acquired %d writers", n)
	}
	checks := 0
	for _, q := range dueTrace() {
		if strings.HasPrefix(q, "SELECT c.album_id,c.release_revision") {
			if !strings.Contains(q, "WHERE c.album_id=? AND") || strings.Contains(q, "ORDER BY") {
				t.Fatalf("writer re-scanned settled prefix: %s", q)
			}
			checks++
		}
	}
	if checks != 1 || readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete'`, last) != 1 {
		t.Fatal("one-candidate PK recheck did not complete the due album")
	}
}

type readPickResult struct {
	done bool
	err  error
}

// The gate holder commits changes after the read-pick has finished. The worker
// must use only current facts and must never repeat the settled-set scan inside
// its writer. Both the losing candidate and remaining-work wake are exercised.
func TestProviderReadPicksMBRechecksOnlyChosenAlbum(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, change := range []string{"settled", "deleted", "pin_mismatch", "new_pin", "membership", "cursor", "policy"} {
		t.Run(change, func(t *testing.T) {
			db, names := mbLaneDB(t)
			defer db.Close()
			c := catalogtest.New(t, db)
			first, song := names["album"].ID, names["song"].ID
			second := c.Album(names["artist"], "Remaining", 2020)
			release := readPickRelease(t, db, "Old pin")
			next := readPickRelease(t, db, "New pin")
			readPickPin(t, db, first, release, "pending")
			readPickPin(t, db, second.ID, release, "pending")
			mbCurrentExec(t, db, `UPDATE mb_jobs SET status='matched',attempts=3,next_attempt='later',error='old',lease='old',lease_until='later' WHERE kind='song'`)
			generation := readPickCount(t, db, `SELECT generation FROM mb_jobs WHERE kind='song' AND entity_id=?`, song)
			ctx := readPickContext(t)
			hold, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
			if err != nil {
				t.Fatal(err)
			}
			defer hold.Rollback()
			ctx, trace := dbwork.TraceQueryPlans(ctx)
			before := backgroundGateAcquisitions()
			results := make(chan readPickResult, 1)
			go func() {
				done, err := New(db, "").mbReconcileAlbums(ctx)
				results <- readPickResult{done, err}
			}()
			waitForWriterWaiter(t, dbwork.ClassBackgroundMedia)
			if got := trace(); len(got) != 1 || !strings.HasPrefix(got[0], "SELECT c.album_id FROM") {
				t.Fatalf("read did not finish before waiting for the writer: %v", got)
			}
			if dbwork.Snapshots().Held != 0 {
				t.Fatal("read snapshot still held at writer admission")
			}
			exec := func(q string, args ...any) {
				t.Helper()
				if _, err := hold.Tx().ExecContext(ctx, q, args...); err != nil {
					t.Fatal(err)
				}
			}
			pin := func(intent mbFixturePinIntent, q string, args ...any) {
				t.Helper()
				if err := mbFixturePinTx(hold.Tx(), intent, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, q, args...)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			wantDone, wantQueued := true, true
			switch change {
			case "settled":
				exec(`UPDATE mb_album_reconciliations SET status='complete' WHERE album_id=?`, first)
				wantDone, wantQueued = false, false
			case "deleted":
				exec(`DELETE FROM mb_album_reconciliations WHERE album_id=?`, first)
				wantDone, wantQueued = false, false
			case "pin_mismatch":
				pin(mbFixturePinIntent{Link: "album", Entity: first, NewRelease: next}, `UPDATE mb_album_links SET release_revision=? WHERE album_id=?`, next, first)
				wantDone, wantQueued = false, false
			case "new_pin":
				pin(mbFixturePinIntent{Link: "album", Entity: first, NewRelease: next}, `UPDATE mb_album_links SET release_revision=? WHERE album_id=?`, next, first)
				exec(`UPDATE mb_album_reconciliations SET release_revision=?,cursor='' WHERE album_id=?`, next, first)
			case "membership":
				exec(`UPDATE mb_album_reconciliations SET cursor=? WHERE album_id=?`, strconv.FormatInt(song, 10), first)
				exec(`UPDATE mb_album_heads SET membership_revision=membership_revision+1 WHERE album_id=?`, first)
			case "cursor":
				exec(`UPDATE mb_album_reconciliations SET cursor=? WHERE album_id=?`, strconv.FormatInt(song, 10), first)
				wantQueued = false
			case "policy":
				// Reconciliation is local work with no policy predicate today;
				// disabling provider fetches must not change that behavior.
				exec(`UPDATE mb_provider_policies SET enabled=0`)
			}
			// Subscribe exactly as Service.Run does for MB. Observe the real
			// commit wake, then consume remaining work in a fresh read-pick.
			signal := worker.NewSignal()
			unregister := dbwork.WakeOnTables(signal, "mb_*")
			defer unregister()
			wakeCtx, cancelWake := context.WithCancel(ctx)
			passes := make(chan struct{}, 2)
			stopped := make(chan struct{})
			go func() {
				defer close(stopped)
				worker.RunWith(wakeCtx, "test.provider-read-pick", signal, nil, func(context.Context) time.Duration {
					passes <- struct{}{}
					return 0
				})
			}()
			<-passes
			defer func() { cancelWake(); <-stopped }()
			if err := hold.Commit(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-passes:
			case <-ctx.Done():
				t.Fatal("candidate-changing commit did not wake the metadata subscription")
			}
			result := <-results
			if result.err != nil || result.done != wantDone {
				t.Fatal(result)
			}
			if n := backgroundGateAcquisitions() - before; n != 1 {
				t.Fatalf("recheck acquired %d writers", n)
			}
			picks, checks := 0, 0
			for _, q := range trace() {
				if strings.Contains(q, "FROM mb_album_reconciliations c") {
					if strings.Contains(q, "WHERE c.album_id=? AND (c.status='pending' OR c.membership_revision<>h.membership_revision)") {
						checks++
					} else if strings.Contains(q, "ORDER BY c.album_id LIMIT 1") {
						picks++
					} else {
						t.Fatalf("unexpected reconciliation search: %s", q)
					}
				}
			}
			if picks != 1 || checks != 1 {
				t.Fatalf("want one ungated pick and one PK recheck: %v", trace())
			}
			wantGeneration := generation
			if wantQueued {
				wantGeneration++
			}
			if got := readPickCount(t, db, `SELECT generation FROM mb_jobs WHERE kind='song' AND entity_id=?`, song); got != wantGeneration {
				t.Fatalf("stale cursor/membership/pin applied: generation=%d want=%d", got, wantGeneration)
			}
			if wantQueued && readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='song' AND entity_id=? AND status='pending' AND attempts=0 AND next_attempt='' AND error='' AND lease='' AND lease_until=''`, song) != 1 {
				t.Fatal("requeued job fields changed")
			}
			if change == "membership" && readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations c JOIN mb_album_heads h ON h.album_id=c.album_id WHERE c.album_id=? AND c.membership_revision=h.membership_revision AND c.status='complete'`, first) != 1 {
				t.Fatal("current membership not recorded")
			}
			if done, err := New(db, "").mbReconcileAlbums(ctx); err != nil || !done {
				t.Fatal("remaining album stranded after wake", done, err)
			}
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete'`, second.ID); n != 1 {
				t.Fatal("remaining album not reconciled")
			}
		})
	}
}

func TestProviderReadPicksMBBatchAndCursorRemainIdentical(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	for i := 0; i < 100; i++ {
		c.Song(names["album"], i+10, fmt.Sprintf("/music/extra-%03d.flac", i), "Track")
	}
	release := readPickRelease(t, db, "Batch")
	readPickPin(t, db, names["album"].ID, release, "pending")
	mbCurrentExec(t, db, `UPDATE mb_jobs SET status='matched',generation=7,revision=9,attempts=3,error='old',next_attempt='later',lease='old',lease_until='later' WHERE kind='song'`)
	s := New(db, "")
	// 101 songs with jobs: each turn requeues one writer-sized quantum of 32.
	passes := []int64{32, 64, 96, 101}
	for pass, want := range passes {
		done, err := s.mbReconcileAlbums(readPickContext(t))
		if err != nil || !done {
			t.Fatal(pass, done, err)
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM mb_jobs WHERE kind='song' AND status='pending' AND generation=8 AND revision=10 AND attempts=0 AND error='' AND next_attempt='' AND lease='' AND lease_until=''`); n != want {
			t.Fatalf("pass %d requeued %d songs, want %d", pass, n, want)
		}
		status := "pending"
		if pass == len(passes)-1 {
			status = "complete"
		}
		if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status=? AND revision=? AND CAST(cursor AS INTEGER)=(SELECT entity_id FROM catalog_songs WHERE album_id=? ORDER BY entity_id LIMIT 1 OFFSET ?)`, names["album"].ID, status, pass+2, names["album"].ID, want-1); n != 1 {
			t.Fatal("batch cursor/status/revision changed")
		}
	}
}

// Traverse a settled prefix without the gate, then fetch the same ordered eight
// episodes. Snapshot release, terminal empty credits and the retry cap matter.
func TestProviderReadPicksTVDBFetchesAfterReleasingSnapshot(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	c := catalogtest.New(t, db)
	for i := 0; i < 96; i++ {
		item := c.Episode(names["show"], names["season"], i+10, fmt.Sprintf("/media/settled-%03d.mkv", i))
		mbCurrentExec(t, db, `INSERT INTO provider_evidence VALUES(?,'tvdb',?,'{}','now')`, item.ID, i+1000)
		mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,?,'now',0,0) ON CONFLICT(item_id,provider_id) DO UPDATE SET credits=excluded.credits,attempts=excluded.attempts`, item.ID, i+1000)
	}
	var picked []catalogtest.Item
	for i := 0; i < 10; i++ {
		item := c.Episode(names["show"], names["season"], i+110, fmt.Sprintf("/media/due-%03d.mkv", i))
		picked = append(picked, item)
		mbCurrentExec(t, db, `INSERT INTO provider_evidence VALUES(?,'tvdb',?,'{}','now')`, item.ID, i+2000)
	}
	mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,2000,'now',NULL,5),(?,2001,'now',NULL,4) ON CONFLICT(item_id,provider_id) DO UPDATE SET credits=excluded.credits,attempts=excluded.attempts`, picked[0].ID, picked[1].ID)
	mbCurrentExec(t, db, `INSERT INTO tvdb_episode_credit_evidence VALUES(?,9999,'now',0,0)`, names["one"].ID)
	ctx := readPickContext(t)
	hold, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	fetched := make(chan int64, 8)
	s := New(db, "")
	s.tvdb = tvdbEpisodeFixture{characters: func(_ context.Context, id int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		if dbwork.Snapshots().Held != 0 {
			return nil, errors.New("snapshot survived into provider fetch")
		}
		fetched <- id
		if id == 2001 {
			return nil, errors.New("deleted upstream episode")
		}
		return []metadataprovider.TVDBEpisodeCharacter{{PersonID: id, PersonName: "Guest", Type: "Actor", Character: "Captain"}}, nil
	}}
	before := backgroundGateAcquisitions()
	results := make(chan error, 1)
	go func() { results <- s.tvdbEpisodeCreditsStep(ctx) }()
	for id := int64(2001); id <= 2008; id++ {
		select {
		case got := <-fetched:
			if got != id {
				t.Fatalf("fetch order=%d want=%d", got, id)
			}
		case <-ctx.Done():
			t.Fatal("read-pick/fetch blocked on catalogue writer", ctx.Err())
		}
	}
	waitForWriterWaiter(t, dbwork.ClassBackgroundMedia)
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-results; err != nil {
		t.Fatal(err)
	}
	if n := backgroundGateAcquisitions() - before; n != 1 {
		t.Fatalf("read-pick plus publication acquired %d writers", n)
	}
	if n := readPickCount(t, db, `SELECT attempts FROM tvdb_episode_credit_evidence WHERE item_id=? AND provider_id=2001`, picked[1].ID); n != 5 {
		t.Fatal("attempt boundary changed", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE credits=1 AND attempts=0`); n != 7 {
		t.Fatal("successful evidence changed", n)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE provider_id=9999`); n != 0 {
		t.Fatal("original orphan cleanup lost")
	}
	for i := 2; i <= 8; i++ {
		provider := strconv.Itoa(2000 + i)
		var key, record string
		var ordinal int
		if err := db.QueryRow(`SELECT c.credit_id,p.accepted_record_reference,c.ord FROM catalog_credits c JOIN credit_source_rows r ON r.source_row_id=c.source_row_id JOIN credit_source_publications p ON p.entity_id=c.entity_id AND p.provider=c.provider WHERE c.entity_id=? AND c.provider='tvdb'`, picked[i].ID).Scan(&key, &record, &ordinal); err != nil {
			t.Fatal(err)
		}
		if key != provider+":0" || record != "tvdb:episode:"+provider || ordinal != 0 {
			t.Fatal("credit identity/provenance changed", key, record, ordinal)
		}
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE item_id=? AND (credits IS NOT NULL OR attempts<>0)`, picked[9].ID); n != 0 {
		t.Fatal("page exceeded eight episodes")
	}
}

func TestProviderReadPicksTVDBPublicationKeepsLookupFence(t *testing.T) {
	db, _ := tvdbDB(t)
	defer db.Close()
	names := tvdbNames(t, db)
	mbCurrentExec(t, db, `INSERT INTO provider_evidence VALUES(?,'tvdb',100,'{}','now')`, names["one"].ID)
	ctx := readPickContext(t)
	hold, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Rollback()
	s := New(db, "")
	s.tvdb = tvdbEpisodeFixture{characters: func(context.Context, int64) ([]metadataprovider.TVDBEpisodeCharacter, error) {
		return []metadataprovider.TVDBEpisodeCharacter{{PersonID: 1, PersonName: "Guest", Type: "Actor"}}, nil
	}}
	results := make(chan error, 1)
	go func() { results <- s.tvdbEpisodeCreditsStep(ctx) }()
	waitForWriterWaiter(t, dbwork.ClassBackgroundMedia)
	if _, err := hold.Tx().ExecContext(ctx, `UPDATE metadata_runtime_settings SET online_lookups=0,lookup_epoch=lookup_epoch+1 WHERE singleton=1`); err != nil {
		t.Fatal(err)
	}
	if err := hold.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-results; !errors.Is(err, localonly.Err) {
		t.Fatalf("old lookup epoch published: %v", err)
	}
	if readPickCount(t, db, `SELECT count(*) FROM tvdb_episode_credit_evidence WHERE credits IS NOT NULL OR attempts<>0`) != 0 || readPickCount(t, db, `SELECT count(*) FROM catalog_credits WHERE provider='tvdb'`) != 0 {
		t.Fatal("rejected publication wrote credits/results/attempts")
	}
}
