package persistence

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// This deliberately uses Open only: explicit installer calls would conceal a
// missing production dependency or incorrect initializer order.
func TestOpenCurrentPublicationAndOriginComposition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	path := filepath.Join(t.TempDir(), "current.sqlite")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if db != nil {
			db.Close()
		}
	}()
	library := persistenceLibrary(t, db, "lib", "TV", "tv", "/synthetic")
	var show, episode int64
	persistenceWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		show, err = persistenceShowTx(ctx, tx, library, "show", "Show", 2000)
		if err != nil {
			return err
		}
		episode, err = persistenceEpisodeTx(ctx, tx, library, show, "show", 1, 1, "Episode")
		if err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO metadata_jobs(item_id) VALUES(?)`, episode)
		return err
	})
	for _, table := range []string{"tvdb_jobs", "tvdb_publication_heads", "tvdb_provider_policies", "metadata_publication_heads", "metadata_work", "playback_origin_roots", "playback_origin_shows", "playback_origin_items"} {
		var n int
		if err = db.QueryRow("SELECT count(*) FROM " + table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("%s: count=%d error=%v", table, n, err)
		}
	}
	var before string
	if err = db.QueryRow(`SELECT incarnation FROM playback_origin_items WHERE id=?`, episode).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`UPDATE tvdb_jobs SET status='complete',revision=23 WHERE show_id=?`, show); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var after, status string
	var revision int
	if err = db.QueryRow(`SELECT incarnation FROM playback_origin_items WHERE id=?`, episode).Scan(&after); err != nil || after != before {
		t.Fatal("reopen replaced current lifetime", after, err)
	}
	if err = db.QueryRow(`SELECT status,revision FROM tvdb_jobs WHERE show_id=?`, show).Scan(&status, &revision); err != nil || status != "complete" || revision != 23 {
		t.Fatal("reopen reset current job", status, revision, err)
	}
	var obsolete int
	if err = db.QueryRow(`SELECT count(*) FROM configuration WHERE key IN('tvdb_backfill_v1','metadata_publication_backfill_v1')`).Scan(&obsolete); err != nil || obsolete != 0 {
		t.Fatal("obsolete backfill marker", obsolete, err)
	}
}
