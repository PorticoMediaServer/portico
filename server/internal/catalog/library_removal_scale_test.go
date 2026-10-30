package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// removalCost runs batch until it reports done and returns the most SQLite
// steps any one batch read and the steps of all of them together.
func removalCost(t *testing.T, c *catalogtest.Catalog, batch func(context.Context, *sql.Tx) (bool, error)) (most, total int64, batches int) {
	t.Helper()
	for {
		ctx, cost := dbwork.Measure(context.Background())
		done := false
		if err := dbwork.WithWriteTx(ctx, c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
			var e error
			done, e = batch(ctx, tx)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		steps := cost().VMSteps
		most = max(most, steps)
		total += steps
		batches++
		if done {
			return most, total, batches
		}
		if batches > 100000 {
			t.Fatal("removal did not finish")
		}
	}
}

// folderWithFiles makes a library with one folder holding n files and
// returns the folder's id.
func folderWithFiles(t *testing.T, c *catalogtest.Catalog, n int) string {
	t.Helper()
	c.Library("m", "Movies", "movie", "/m")
	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='m'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		for i := 0; i < n; i++ {
			path := fmt.Sprintf("/m/f%08d.mkv", i)
			_, token, err := compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: path, Size: 1, ModifiedNS: 1, Container: "mkv", Duration: 60})
			if err != nil {
				return err
			}
			if _, err = tx.ExecContext(ctx, `INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns)
 SELECT ?,id,?,incarnation,?,'r','{}',1,1 FROM library_sources WHERE id=?`, fmt.Sprintf("object:%08d", i), token, path[3:], source); err != nil {
				return err
			}
		}
		return nil
	})
	c.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, source)
	return source
}

// Removing a folder walks its files once. Each batch reads a bounded number
// of rows however much of the folder is already done, so the whole removal
// costs in proportion to the folder: at twice the files, about twice the
// steps, and no batch dearer than at half the size.
func TestFolderRemovalCostIsLinear(t *testing.T) {
	cost := func(n int) (int64, int64) {
		c := catalogtest.Open(t)
		source := folderWithFiles(t, c, n)
		most, total, batches := removalCost(t, c, func(ctx context.Context, tx *sql.Tx) (bool, error) { return removeFolderBatchTx(ctx, tx, source) })
		if left := rows(t, c, `SELECT count(*) FROM inventory_objects WHERE state<>'missing'`); left != 0 {
			t.Fatalf("%d files were not marked missing", left)
		}
		if n := rows(t, c, `SELECT count(*) FROM catalog_removal_progress`); n != 0 {
			t.Fatalf("the finished removal left its place behind: %d", n)
		}
		t.Logf("%d files: %d batches, %d steps, at most %d in one batch", n, batches, total, most)
		return most, total
	}
	smallMost, smallTotal := cost(400)
	largeMost, largeTotal := cost(800)
	if largeMost > smallMost*6/5 {
		t.Fatalf("one batch grew with the folder: %d steps at 800 files against %d at 400", largeMost, smallMost)
	}
	if largeTotal > smallTotal*5/2 {
		t.Fatalf("the removal is not linear: %d steps at 800 files against %d at 400", largeTotal, smallTotal)
	}
}

// A folder added back while it is being removed stops the removal, keeps the
// files it had not reached, and a later removal starts again from the front.
func TestFolderRemovalStopsWhenTheFolderIsAddedBack(t *testing.T) {
	c := catalogtest.Open(t)
	source := folderWithFiles(t, c, 100)
	batch := func() bool {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			done, e = removeFolderBatchTx(ctx, tx, source)
			return e
		})
		return done
	}
	if batch() || batch() {
		t.Fatal("a 100-file folder finished in two batches")
	}
	marked := rows(t, c, `SELECT count(*) FROM inventory_objects WHERE state='missing'`)
	if marked == 0 || rows(t, c, `SELECT count(*) FROM catalog_removal_progress WHERE kind='folder' AND target=?`, source) != 1 {
		t.Fatalf("the removal kept no place: %d marked", marked)
	}
	c.Exec(`UPDATE library_sources SET enabled=1 WHERE id=?`, source)
	if !batch() {
		t.Fatal("the folder was added back but its removal carried on")
	}
	if n := rows(t, c, `SELECT count(*) FROM inventory_objects WHERE state='missing'`); n != marked {
		t.Fatalf("files were marked missing after the folder came back: %d, was %d", n, marked)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_removal_progress`); n != 0 {
		t.Fatalf("the stopped removal left its place behind: %d", n)
	}
	// Removed again: the walk starts at the front, so nothing is skipped.
	c.Exec(`UPDATE inventory_objects SET state='available',missing_since=''`)
	c.Exec(`UPDATE library_sources SET enabled=0 WHERE id=?`, source)
	for !batch() {
	}
	if n := rows(t, c, `SELECT count(*) FROM inventory_objects WHERE state<>'missing'`); n != 0 {
		t.Fatalf("a second removal skipped %d files", n)
	}
}

// Removing a reading library, whose books do not play, never reads the books
// again in every batch: no batch costs more at twice the books, and the whole
// removal grows with the library, not with its square.
func TestReadingLibraryRemovalCostIsLinear(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	cost := func(n int) (int64, int64) {
		c := catalogtest.Open(t)
		handle := c.Library("r", "Reading", "book", "/r")
		// Only removal is measured below. Build the same canonical entities in
		// bounded fixture batches instead of committing and looking up an unused
		// public ID once per book. Keep both scale points and every removal check.
		for start := 0; start < n; start += 128 {
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				for i := start; i < min(start+128, n); i++ {
					if _, _, err := compactcatalog.UpsertEntityTx(ctx, tx, compactcatalog.Entity{Library: handle, Kind: 20, Key: fmt.Sprintf("ebook:%d", i), Title: fmt.Sprintf("Book %d", i)}); err != nil {
						return err
					}
				}
				return nil
			})
		}
		c.Drain()
		c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='r'`)
		most, total, batches := removalCost(t, c, func(ctx context.Context, tx *sql.Tx) (bool, error) { return removeLibraryBatchTx(ctx, tx, "r") })
		if n := rows(t, c, `SELECT count(*) FROM catalog_entities`) + rows(t, c, `SELECT count(*) FROM libraries WHERE id='r'`); n != 0 {
			t.Fatalf("the reading library was not removed: %d rows left", n)
		}
		if n := rows(t, c, `SELECT count(*) FROM catalog_removal_progress`); n != 0 {
			t.Fatalf("the finished removal left its place behind: %d", n)
		}
		t.Logf("%d books: %d batches, %d steps, at most %d in one batch", n, batches, total, most)
		return most, total
	}
	smallMost, smallTotal := cost(300)
	largeMost, largeTotal := cost(600)
	if largeMost > smallMost*6/5 {
		t.Fatalf("one batch grew with the library: %d steps at 600 books against %d at 300", largeMost, smallMost)
	}
	if largeTotal > smallTotal*5/2 {
		t.Fatalf("the removal is not linear: %d steps at 600 books against %d at 300", largeTotal, smallTotal)
	}
}

// What hangs off a library's folders in numbers goes in bounded slices before
// the folders do, so deleting a folder row cascades over nothing large.
func TestLibraryRemovalDrainsFolderCachesBeforeTheFolders(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c := catalogtest.Open(t)
	c.Library("m", "Movies", "movie", "/m")
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='m'`).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	c.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<4500)
 INSERT INTO inventory_directory_cache(source_id,incarnation,relative_path,parent_path) SELECT ?,?,printf('d%05d',i),'.' FROM n`, source, incarnation)
	c.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<4500)
 INSERT INTO inventory_sidecars(source_id,incarnation,relative_path,directory_path,revision,seen_job) SELECT ?,?,printf('d%05d/a.nfo',i),printf('d%05d',i),'r','j' FROM n`, source, incarnation)
	c.Exec(`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM n WHERE i<4500)
 INSERT INTO inventory_path_notices(source_id,relative_path,created_ms) SELECT ?,printf('d%05d',i),1 FROM n`, source)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE library_id='m'`)
	var refs []bulkReference
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		refs, err = bulkReferences(ctx, tx)
		return err
	})
	drained := map[string]bool{}
	for _, ref := range refs {
		drained[ref.table] = true
	}
	for _, table := range []string{"inventory_directory_cache", "inventory_sidecars", "inventory_path_notices"} {
		if !drained[table] {
			t.Fatalf("%s is not drained before its folder row; drained: %v", table, refs)
		}
	}
	for {
		done := false
		before := rows(t, c, `SELECT count(*) FROM inventory_directory_cache`) + rows(t, c, `SELECT count(*) FROM inventory_sidecars`) + rows(t, c, `SELECT count(*) FROM inventory_path_notices`)
		folder := rows(t, c, `SELECT count(*) FROM library_sources WHERE library_id='m'`)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var e error
			done, e = removeLibraryBatchTx(ctx, tx, "m")
			return e
		})
		after := rows(t, c, `SELECT count(*) FROM inventory_directory_cache`) + rows(t, c, `SELECT count(*) FROM inventory_sidecars`) + rows(t, c, `SELECT count(*) FROM inventory_path_notices`)
		if before-after > 2000 {
			t.Fatalf("one batch deleted %d cached rows", before-after)
		}
		if folder == 1 && rows(t, c, `SELECT count(*) FROM library_sources WHERE library_id='m'`) == 0 && before != 0 {
			t.Fatalf("the folder row went with %d cached rows still under it", before)
		}
		if done {
			break
		}
	}
	if n := rows(t, c, `SELECT count(*) FROM libraries WHERE id='m'`); n != 0 {
		t.Fatal("the library was not removed")
	}
}
