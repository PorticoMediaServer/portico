package compactcatalog_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// Measure only the path worker, including its EOF probes. Fixture creation and
// availability derivation are outside this counter; no filesystem IO is used.
func measurePathDrain(t *testing.T, f *completeFixture) (steps, raw int, cost dbwork.Cost) {
	t.Helper()
	ctx, read := dbwork.Measure(context.Background())
	for steps < 10000 {
		n := 0
		err := dbwork.WithWriteTxContext(ctx, f.c.DB, dbwork.ClassBackgroundMedia, func(ctx context.Context, tx *sql.Tx) error {
			var err error
			n, err = compactcatalog.StepReadingPaths(ctx, tx, 7)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
		if n < 0 || n > 7 {
			t.Fatalf("raw budget exceeded: %d", n)
		}
		steps++
		raw += n
		if n == 0 {
			return steps, raw, read()
		}
	}
	t.Fatal("path feed did not become idle")
	return
}

func TestReadingPathFileChangeDoesNotRepublishSiblings(t *testing.T) {
	for _, size := range []int{1, 8, 32} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			f := newCompleteFixture(t, nil, "per-file")
			var files []completePublication
			for i := range size {
				files = append(files, f.add(fmt.Sprintf("Author/Series/Book %03d.epub", i+1), "epub", 0))
			}
			f.settle()
			f.seal()
			steps, raw, cost := measurePathDrain(t, f)
			t.Logf("PATH_WORK phase=first-import files=%d steps=%d raw=%d statements=%d vm=%d", size, steps, raw, cost.Statements, cost.VMSteps)
			before := make([]int64, len(files))
			for i, file := range files {
				before[i] = scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=? AND asset_id=?`, file.item, file.asset)
			}
			f.c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id=?`, files[0].object)
			f.settle()
			steps, raw, cost = measurePathDrain(t, f)
			t.Logf("PATH_WORK phase=one-file-change files=%d steps=%d raw=%d statements=%d vm=%d", size, steps, raw, cost.Statements, cost.VMSteps)
			if raw != 1 || cost.Statements > 32 {
				t.Fatalf("one-file work grew with siblings: raw=%d statements=%d", raw, cost.Statements)
			}
			if got := scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_head WHERE object_id=? AND source_revision='r2'`, files[0].object); got != 1 {
				t.Fatal("changed file did not publish its own revision", got)
			}
			for i, file := range files[1:] {
				if after := scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=? AND asset_id=?`, file.item, file.asset); after != before[i+1] {
					t.Errorf("unchanged sibling %s republished: %d -> %d", file.object, before[i+1], after)
				}
			}
		})
	}
}

func TestReadingPathOpenScanPublishesFullOwnBundle(t *testing.T) {
	f := newCompleteFixture(t, nil, "open-file")
	p := f.add("Folder Author/Series/Writer - Issue 01 (2026-01).epub", "epub", 0)
	f.settle()
	measurePathDrain(t, f)
	got := completeValues(t, f.c, p.item)
	if got.Author == nil || got.Author.Value != "Writer" || got.Series == nil || got.Series.Value != "Series" || got.SeriesNumber == nil || got.SeriesNumber.Value != 1 || got.IssueDate == nil || got.IssueDate.Value != "2026-01" {
		t.Fatal("open scan lost complete lexical bundle", got)
	}
	var phase string
	if err := f.c.DB.QueryRow(`SELECT phase FROM reading_path_source_barrier WHERE source_id=?`, f.source).Scan(&phase); err != nil || phase != "open" {
		t.Fatal("path inference changed traversal authority", phase, err)
	}
}

func TestReadingPathAvailabilityDebtSurvivesIdleAndDatabaseRestart(t *testing.T) {
	f := newCompleteFixture(t, nil, "pending-file")
	p := f.add("Author/Series/Book 01.epub", "epub", 0)
	measurePathDrain(t, f)
	if scalar(t, f.c.DB, `SELECT pending FROM reading_path_object_keys WHERE object_id=?`, p.object) != 1 || scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_head WHERE item_id=?`, p.item) != 0 {
		t.Fatal("unsettled availability was acknowledged")
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var before, after int64
		if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&before); err != nil {
			return err
		}
		for range 3 {
			if n, err := compactcatalog.StepReadingPaths(ctx, tx, 7); err != nil || n != 0 {
				t.Fatal("idle debt was polled", n, err)
			}
		}
		if err := tx.QueryRowContext(ctx, `SELECT total_changes()`).Scan(&after); err != nil {
			return err
		}
		if before != after {
			t.Fatal("idle debt wrote to the database", before, after)
		}
		return nil
	})
	var seq int
	var name, path string
	if err := f.c.DB.QueryRow(`PRAGMA database_list`).Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	if err := f.c.DB.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := persistence.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.c.DB = db
	f.settle()
	measurePathDrain(t, f)
	if scalar(t, f.c.DB, `SELECT pending FROM reading_path_object_keys WHERE object_id=?`, p.object) != 0 {
		t.Fatal("settled event lost durable publication debt")
	}
	got := completeValues(t, f.c, p.item)
	if got.Author == nil || got.Author.Value != "Author" || got.Series == nil || got.Series.Value != "Series" {
		t.Fatal(got)
	}
}

func TestReadingPathMissingRetainsAcceptedAndEmptyBundleClears(t *testing.T) {
	f := newCompleteFixture(t, nil, "missing-file")
	p := f.add("Author/Series/Book 01.epub", "epub", 0)
	f.settle()
	measurePathDrain(t, f)
	before := scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item)
	f.c.Exec(`UPDATE inventory_objects SET state='missing' WHERE id=?`, p.object)
	f.c.Exec(`UPDATE catalog_assets SET available=0 WHERE id=?`, p.asset)
	f.c.Exec(`UPDATE catalog_asset_links SET available=0 WHERE entity_id=?`, p.item)
	f.settle()
	measurePathDrain(t, f)
	got := completeValues(t, f.c, p.item)
	if got.Author == nil || got.Author.Value != "Author" || got.Series == nil || got.Series.Value != "Series" || scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item) != before {
		t.Fatal("missing file changed last accepted description", got)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		_, err := compactcatalog.ReadingPublishPathTx(ctx, tx, p.item, p.asset, p.object, "r1", compactcatalog.ReadingValues{})
		if err != nil {
			return err
		}
		return compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
	})
	assertCompleteValue(t, f.c, p, compactcatalog.ReadingValues{})
}

func TestReadingPathChangedBundleRollsBackAndRetriesAtomically(t *testing.T) {
	f := newCompleteFixture(t, nil, "atomic-file")
	p := f.add("Author/Series/Writer - Issue 01 (2026-01).epub", "epub", 0)
	f.settle()
	measurePathDrain(t, f)
	before := scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item)
	f.c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id=?`, p.object)
	f.c.Exec(`CREATE TRIGGER path_publication_pause BEFORE INSERT ON reading_path_head BEGIN SELECT RAISE(ABORT,'resumable lexical publication'); END`)
	err := dbwork.WithWriteTx(context.Background(), f.c.DB, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		_, err := compactcatalog.StepReadingPaths(context.Background(), tx, 7)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "resumable lexical publication") {
		t.Fatal("did not interrupt publication", err)
	}
	if scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item) != before {
		t.Fatal("failed transaction published a partial bundle")
	}
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_object_keys WHERE object_id=? AND source_revision='r1'`, p.object) != 1 {
		t.Fatal("failed transaction acknowledged new witness")
	}
	f.c.Exec(`DROP TRIGGER path_publication_pause`)
	f.settle()
	measurePathDrain(t, f)
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_head WHERE item_id=? AND source_revision='r2' AND author='Writer' AND series='Series' AND series_number=1 AND issue_date='2026-01'`, p.item) != 1 {
		t.Fatal("retry did not publish whole exact bundle")
	}
}

func TestReadingPathUnchangedOtherSourceCannotResurrectSupersededHead(t *testing.T) {
	f := newCompleteFixture(t, nil, "supersession-file")
	p := f.add("Author/Series/Book 01.epub", "epub", 0)
	f.settle()
	measurePathDrain(t, f)
	var token string
	if err := f.c.DB.QueryRow(`SELECT token FROM catalog_assets WHERE id=?`, p.asset).Scan(&token); err != nil {
		t.Fatal(err)
	}
	f.c.Exec(`INSERT INTO library_sources(id,library_id,configured_root,root,identity_confirmed,root_identity) VALUES('overlap',?,?,?,1,'root')`, f.library, filepath.Join(f.root, "Author"), filepath.Join(f.root, "Author"))
	f.c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) SELECT 'overlap-object',id,?,incarnation,?,'newer','{}',1,1 FROM library_sources WHERE id='overlap'`, token, filepath.FromSlash("Series/Book 01.epub"))
	f.settle()
	measurePathDrain(t, f)
	if scalar(t, f.c.DB, `SELECT count(*) FROM reading_path_head WHERE item_id=? AND object_id='overlap-object'`, p.item) != 1 {
		t.Fatal("new source did not supersede original publisher")
	}
	sequence := scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item)
	f.c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?,'source',?) ON CONFLICT DO UPDATE SET requested=requested+1`, f.handle, f.source)
	measurePathDrain(t, f)
	if scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item) != sequence {
		t.Fatal("unchanged old source became a newer publisher")
	}
	f.c.Exec(`UPDATE inventory_objects SET retired=1 WHERE id='overlap-object'`)
	f.settle()
	measurePathDrain(t, f)
	assertCompleteValue(t, f.c, p, compactcatalog.ReadingValues{})
	if scalar(t, f.c.DB, `SELECT publisher_sequence FROM reading_path_head WHERE item_id=?`, p.item) != sequence {
		t.Fatal("retiring winner resurrected overwritten original")
	}
}

func TestReadingPathPerFilePublisherKeepsMetadataAndOwnerPrecedence(t *testing.T) {
	f := newCompleteFixture(t, nil, "precedence-file")
	p := f.add("Folder Author/Series/Writer - Issue 01.epub", "epub", 0)
	f.settle()
	measurePathDrain(t, f)
	f.c.Exec(`INSERT INTO reading_source_values(item_id,asset_id,field,text_value,source,inventory_object_id,source_revision) VALUES(?,?,'author','Embedded','file-metadata',?,'r1')`, p.item, p.asset, p.object)
	if got := completeValues(t, f.c, p.item); got.Author == nil || got.Author.Value != "Embedded" {
		t.Fatal("lexical guess displaced file metadata", got)
	}
	f.c.Exec(`INSERT INTO metadata_owner_fields(kind,entity_id,field,value,automatic_value,locked,actor,observed_at) VALUES('item',?,'author','Owner','Embedded',1,'owner','now')`, p.item)
	f.c.Exec(`UPDATE inventory_objects SET revision='r2' WHERE id=?`, p.object)
	f.settle()
	measurePathDrain(t, f)
	if got := completeValues(t, f.c, p.item); got.Author == nil || got.Author.Value != "Writer" {
		t.Fatal("old file-metadata witness survived a different revision", got)
	}
	f.c.Write(func(ctx context.Context, tx *sql.Tx) error {
		got, err := compactcatalog.ReadingAcceptedValuesTx(ctx, tx, []int64{p.item})
		if err == nil && (got[p.item].Author == nil || got[p.item].Author.Value != "Owner") {
			t.Fatal("owner lost precedence", got)
		}
		return err
	})
}
