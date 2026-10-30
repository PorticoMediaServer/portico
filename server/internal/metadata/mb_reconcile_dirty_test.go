package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"testing"
)

func assertMBReconcileProjection(t *testing.T, db *sql.DB) {
	t.Helper()
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations c WHERE current_pin<>EXISTS(SELECT 1 FROM mb_album_heads h JOIN mb_album_links l ON l.album_id=h.album_id WHERE h.album_id=c.album_id AND l.release_revision=c.release_revision) OR (status<>'pending' AND EXISTS(SELECT 1 FROM mb_album_heads h WHERE h.album_id=c.album_id AND h.membership_revision<>c.membership_revision))`); n != 0 {
		t.Fatalf("%d inconsistent reconciliation rows", n)
	}
}

func TestMBReconcileDirtyProducerTransitions(t *testing.T) {
	for _, recursive := range []int{0, 1} {
		t.Run(fmt.Sprint(recursive), func(t *testing.T) {
			db, names := mbLaneDB(t)
			defer db.Close()
			mbCurrentExec(t, db, fmt.Sprintf(`PRAGMA recursive_triggers=%d`, recursive))
			album := names["album"].ID
			old := readPickRelease(t, db, "First")
			next := readPickRelease(t, db, "Second")
			readPickPin(t, db, album, old, "complete")
			assertMBReconcileProjection(t, db)
			revision := readPickCount(t, db, `SELECT revision FROM mb_album_reconciliations WHERE album_id=?`, album)
			mbCurrentExec(t, db, `UPDATE mb_album_heads SET revision=revision+1 WHERE album_id=?`, album)
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete' AND revision=? AND current_pin=1`, album, revision); n != 1 {
				t.Fatal("revision-only head change enqueued work")
			}
			mbCurrentExec(t, db, `UPDATE mb_album_heads SET membership_revision=membership_revision+1 WHERE album_id=?`, album)
			assertMBReconcileProjection(t, db)
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations c JOIN mb_album_heads h USING(album_id) WHERE c.album_id=? AND c.status='pending' AND c.membership_revision<>h.membership_revision AND c.revision=?`, album, revision); n != 1 {
				t.Fatal("membership did not preserve old revision and pending work")
			}
			mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: album, NewRelease: next}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`UPDATE mb_album_links SET release_revision=? WHERE album_id=?`, next, album)
				return err
			})
			assertMBReconcileProjection(t, db)
			if n := readPickCount(t, db, `SELECT current_pin FROM mb_album_reconciliations WHERE album_id=?`, album); n != 0 {
				t.Fatal("old pin runnable")
			}
			mbCurrentExec(t, db, `UPDATE mb_album_reconciliations SET release_revision=? WHERE album_id=?`, next, album)
			assertMBReconcileProjection(t, db)
			mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: album}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`DELETE FROM mb_album_links WHERE album_id=?`, album)
				return err
			})
			assertMBReconcileProjection(t, db)
			mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: album, NewRelease: next}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`INSERT INTO mb_album_links VALUES(?,?,?,?,'now')`, album, next, mbLaneRelease, mbLaneRelease)
				return err
			})
			assertMBReconcileProjection(t, db)
			mbCurrentExec(t, db, `UPDATE mb_provider_policies SET enabled=0`)
			if done, err := New(db, "").mbReconcileAlbums(readPickContext(t)); err != nil || !done {
				t.Fatal(done, err)
			}
			assertMBReconcileProjection(t, db)
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete' AND current_pin=1`, album); n != 1 {
				t.Fatal("policy-off local reconciliation lost")
			}
			mbCurrentExec(t, db, `DELETE FROM mb_album_heads WHERE album_id=?`, album)
			assertMBReconcileProjection(t, db)
			mbCurrentExec(t, db, `INSERT INTO mb_album_heads(album_id) VALUES(?)`, album)
			assertMBReconcileProjection(t, db)
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if err = mbFixturePinTx(tx, mbFixturePinIntent{Link: "album", Entity: album}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`DELETE FROM mb_album_links WHERE album_id=?`, album)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err = tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			assertMBReconcileProjection(t, db)
			if n := readPickCount(t, db, `SELECT current_pin FROM mb_album_reconciliations WHERE album_id=?`, album); n != 1 {
				t.Fatal("rollback lost runnable bit")
			}
			c := catalogtest.New(t, db)
			second := c.Album(names["artist"], "Deleted album", 2020)
			readPickPin(t, db, second.ID, old, "pending")
			if _, err := db.Exec(`DELETE FROM catalog_entities WHERE id=?`, second.ID); err == nil {
				t.Fatal("physical delete bypassed pin drain")
			}
			mbFixturePin(t, db, mbFixturePinIntent{Link: "album", Entity: second.ID}, func(tx *sql.Tx) error {
				_, err := tx.Exec(`DELETE FROM mb_album_links WHERE album_id=?`, second.ID)
				return err
			})
			mbCurrentExec(t, db, `DELETE FROM catalog_entities WHERE id=?`, second.ID)
			assertMBReconcileProjection(t, db)
			if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=?`, second.ID); n != 0 {
				t.Fatal("cascade retained reconciliation")
			}
		})
	}
}

func TestMBReconcileDirtyPickOmitsSettledAndPinGapPrefixes(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	release := readPickRelease(t, db, "Current")
	gap := readPickRelease(t, db, "Gap")
	readPickPin(t, db, names["album"].ID, release, "complete")
	for i := 0; i < 256; i++ {
		album := c.Album(names["artist"], fmt.Sprintf("Prefix %03d", i), 2020)
		readPickPin(t, db, album.ID, release, "complete")
		if i%2 == 0 {
			mbCurrentExec(t, db, `UPDATE mb_album_reconciliations SET status='pending',release_revision=? WHERE album_id=?`, gap, album.ID)
		}
	}
	assertMBReconcileProjection(t, db)
	// This measures the indexed pick, not lazy background-pool connection setup.
	// Open the pool before its short snapshot lifetime starts under host load.
	background := dbwork.WithClass(context.Background(), dbwork.ClassBackgroundMedia)
	if err := dbwork.ReadHandle(background, db).PingContext(background); err != nil {
		t.Fatal(err)
	}
	ctx, cost := dbwork.Measure(context.Background())
	if done, err := New(db, "").mbReconcileAlbums(ctx); err != nil || done {
		t.Fatal(done, err)
	}
	if got := cost(); got.VMSteps > 250 || got.Transactions > 1 {
		t.Fatalf("idle pick walked excluded prefix: %+v", got)
	}
	last := c.Album(names["artist"], "Due after prefix", 2020)
	readPickPin(t, db, last.ID, release, "pending")
	if done, err := New(db, "").mbReconcileAlbums(readPickContext(t)); err != nil || !done {
		t.Fatal(done, err)
	}
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='complete'`, last.ID); n != 1 {
		t.Fatal("due suffix not served")
	}
}

func TestMBReconcileDirtyRealMembershipProducers(t *testing.T) {
	db, names := mbLaneDB(t)
	defer db.Close()
	c := catalogtest.New(t, db)
	other := c.Album(names["artist"], "Other membership", 2020)
	release := readPickRelease(t, db, "Membership")
	first := names["album"].ID
	readPickPin(t, db, first, release, "complete")
	readPickPin(t, db, other.ID, release, "complete")
	song := c.Song(names["album"], 2, "/music/added.flac", "Added")
	assertMBReconcileProjection(t, db)
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='pending'`, first); n != 1 {
		t.Fatal("song insert did not enqueue album")
	}
	mbCurrentExec(t, db, `UPDATE mb_album_reconciliations SET membership_revision=(SELECT membership_revision FROM mb_album_heads h WHERE h.album_id=mb_album_reconciliations.album_id),status='complete'`)
	mbCurrentExec(t, db, `UPDATE catalog_songs SET album_id=? WHERE entity_id=?`, other.ID, song.ID)
	assertMBReconcileProjection(t, db)
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id IN(?,?) AND status='pending'`, first, other.ID); n != 2 {
		t.Fatal("move did not enqueue both memberships")
	}
	mbCurrentExec(t, db, `UPDATE mb_album_reconciliations SET membership_revision=(SELECT membership_revision FROM mb_album_heads h WHERE h.album_id=mb_album_reconciliations.album_id),status='complete'`)
	mbCurrentExec(t, db, `DELETE FROM catalog_songs WHERE entity_id=?`, song.ID)
	assertMBReconcileProjection(t, db)
	if n := readPickCount(t, db, `SELECT count(*) FROM mb_album_reconciliations WHERE album_id=? AND status='pending'`, other.ID); n != 1 {
		t.Fatal("song delete did not enqueue old membership")
	}
}
