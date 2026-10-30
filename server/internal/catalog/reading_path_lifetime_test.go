package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
)

// Private build/work/scope lifetimes may outlive public authority. Existing
// removal must still finish, without a new refusal, source FK or large cascade.
func TestReadingCompleteLibraryRemovalWithActivatedOverlay(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("path-removal", "Reading", "book", root)
	item := c.Entity(compactcatalog.Entity{Library: lib, Kind: compactcatalog.Ebook, Key: "book", Title: "Book"}, nil)
	asset, token := c.File(item.ID, filepath.Join(root, "Shelf", "Book.epub"), 0)
	var source, inc string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id='path-removal'`).Scan(&source, &inc); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO reading_publications VALUES(?,?,'epub')`, item.ID, asset)
	c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES('removal-object',?,?,?,'Shelf/Book.epub','r','{}',1,1)`, source, token, inc)
	c.Exec(`INSERT INTO reading_path_scope(library_id,source_id,incarnation,root_prefix,directory_key,completed,queued) VALUES(?,?,?,?,'Shelf',1,0)`, lib, source, inc, root+string(filepath.Separator))
	c.Exec(`INSERT OR IGNORE INTO reading_path_inputs(library_id,revision) VALUES(?,1)`, lib)
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		if err := compactcatalog.ReadingNormalizeObjectTx(ctx, tx, "removal-object"); err != nil {
			return err
		}
		return compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
	})
	c.Exec(`UPDATE reading_path_clock SET sequence=2`)

	c.Exec(`INSERT INTO reading_path_build(scope_id,desired,input_revision,source_generation,root_identity,job_id,phase,publish_sequence) VALUES(1,1,1,1,'r','job','flatten',2)`)
	c.Exec(`UPDATE reading_path_slot SET build_id=1,overlay_id=1`)
	c.Exec(`INSERT INTO reading_path_stage(build_id,item_id,asset_id,object_id,source_revision,author,author_source) VALUES(1,?,?,'removal-object','r','Shelf','folder')`, item.ID, asset)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	done := false
	for i := 0; i < 100 && !done; i++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = removeLibraryBatchTx(ctx, tx, "path-removal")
			return err
		})
	}
	if !done {
		t.Fatal("private Reading work blocked existing removal")
	}
	for i := 0; i < 100; i++ {
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			_, err := compactcatalog.StepReadingPaths(ctx, tx, 7)
			return err
		})
	}
	for _, table := range []string{"reading_path_head", "reading_path_stage", "reading_path_scope_entities", "reading_path_source_barrier", "reading_path_block", "reading_path_scope", "reading_path_inputs", "reading_path_build", "reading_path_dirty_work"} {
		var n int
		if err := c.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil || n != 0 {
			t.Fatalf("orphan %s=%d err=%v", table, n, err)
		}
	}
}

func TestReadingCompleteUnchangedObservationOnlyAdvancesSeenJob(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	c.Library("rescan", "Reading", "book", root)
	svc := New(c.DB)
	sources, err := svc.LibrarySources(context.Background(), "rescan")
	if err != nil || len(sources) != 1 {
		t.Fatal(err)
	}
	source := sources[0]
	scan := func(job string) {
		c.Exec(`INSERT INTO jobs(id,library_id,status,created_at) VALUES(?,'rescan','running','now')`, job)
		c.Exec(`INSERT INTO inventory_runs(job_id,source_id,source_generation,root_incarnation,root_identity,policy_revision) VALUES(?,?,?,?,?,1)`, job, source.ID, source.Generation, source.Incarnation, source.RootIdentity)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			for i := 0; i < 3; i++ {
				relative := fmt.Sprintf("Author/Series/Series %02d.epub", i)
				v := storage.Snapshot{Name: filepath.Base(relative), Path: filepath.Join(root, relative), Size: 100, ModifiedNS: 1, ObjectIdentity: fmt.Sprintf("inode:%d", i)}
				if _, err := svc.observeInventoryTx(ctx, tx, job, source, relative, v, "", ScanPolicy{Tier: "file_list_only"}, nil); err != nil {
					return err
				}
			}
			return compactcatalog.FlushReadingPathSemanticTx(ctx, tx)
		})
		c.Exec(`DELETE FROM catalog_dirty WHERE domain IN(20,28)`)
		c.Exec(`UPDATE inventory_runs SET phase='reconciling',authoritative=1 WHERE job_id=?`, job)
		c.Exec(`UPDATE inventory_runs SET phase='sidecars' WHERE job_id=?`, job)
		for i := 0; i < 300; i++ {
			var pending bool
			if err := c.DB.QueryRow(`SELECT EXISTS(SELECT 1 FROM reading_path_scope WHERE queued=1) OR EXISTS(SELECT 1 FROM reading_path_dirty_work) OR EXISTS(SELECT 1 FROM reading_path_build)`).Scan(&pending); err != nil {
				t.Fatal(err)
			}
			if !pending {
				return
			}
			c.Write(func(ctx context.Context, tx *sql.Tx) error {
				_, err := compactcatalog.StepReadingPaths(ctx, tx, 7)
				return err
			})
		}
		t.Fatal("observation did not settle")
	}
	state := func() (desired, sequence int64) {
		if err := c.DB.QueryRow(`SELECT sum(desired),(SELECT sequence FROM reading_path_clock) FROM reading_path_scope`).Scan(&desired, &sequence); err != nil {
			t.Fatal(err)
		}
		return
	}
	scan("first-rescan")
	desired, seq := state()
	scan("second-rescan")
	after, afterSeq := state()
	if after != desired || afterSeq != seq {
		t.Fatalf("unchanged observation rebuilt siblings: desired %d->%d sequence %d->%d", desired, after, seq, afterSeq)
	}
	var seen int
	if err := c.DB.QueryRow(`SELECT count(*) FROM inventory_objects WHERE seen_job='second-rescan'`).Scan(&seen); err != nil || seen != 3 {
		t.Fatalf("seen bookkeeping did not advance: %d %v", seen, err)
	}
}
