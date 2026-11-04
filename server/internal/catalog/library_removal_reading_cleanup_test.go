package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

func readingCleanupDrain(t *testing.T, c *catalogtest.Catalog, library string, limit int) int {
	t.Helper()
	for i := 0; i < limit; i++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = removeLibraryBatchTx(ctx, tx, library)
			return err
		})
		if done {
			return i + 1
		}
	}
	t.Fatalf("library %q did not finish in %d bounded steps", library, limit)
	return 0
}

func readingCleanupCount(t *testing.T, c *catalogtest.Catalog, query string, args ...any) int {
	t.Helper()
	return rows(t, c, query, args...)
}

type readingCleanupBook struct {
	item, asset int64
	object      string
}

func readingCleanupBooks(t *testing.T, c *catalogtest.Catalog, library int64, root, directory string, n int) []readingCleanupBook {
	t.Helper()
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, library).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	var books []readingCleanupBook
	for i := range n {
		item := c.Entity(compactcatalog.Entity{Library: library, Kind: compactcatalog.Ebook, Key: fmt.Sprintf("ebook:%s:%d", directory, i), Title: fmt.Sprint(i)}, nil)
		relative := fmt.Sprintf("%s/Book %d.epub", directory, i)
		asset, token := c.File(item.ID, filepath.Join(root, filepath.FromSlash(relative)), 0)
		object := fmt.Sprintf("cleanup-%s-%d", directory, i)
		c.Exec(`INSERT INTO reading_publications VALUES(?,?,'epub')`, item.ID, asset)
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,?,?,?,?,'r','{}',1,1)`, object, source, token, incarnation, filepath.FromSlash(relative))
		c.Exec(`DELETE FROM reading_path_availability_pending WHERE entity_id=?`, item.ID)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.ReadingNormalizeObjectTx(ctx, tx, object)
		})
		books = append(books, readingCleanupBook{item.ID, asset, object})
	}
	return books
}

func TestReadingRemovalPhaseFiveDrainsTextObjectResidue(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	books := readingCleanupBooks(t, c, lib, root, "Shelf", 1)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	// Resume at phase 5: the earlier removal phases cannot erase this
	// retained per-object witness before its own residue unit sees the key.
	c.Exec(`INSERT INTO catalog_removal_progress(kind,target,phase) VALUES('library','r',5)`)
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_object_keys WHERE library_id=?`, lib); n != 1 {
		t.Fatal("missing actual per-object residue", n, books)
	}
	for range 100 {
		err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			_, err := removeLibraryBatchTx(context.Background(), tx, "r")
			return err
		})
		if err != nil {
			t.Fatal("phase-5 per-object residue cannot drain", err)
		}
		if readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_object_keys WHERE library_id=?`, lib) == 0 {
			return
		}
	}
	t.Fatal("object residue retained")
}

func TestReadingRemovalOwnerDrainsRetiredLifetimeAndResidue(t *testing.T) {
	c := catalogtest.Open(t)
	keepRoot, root := t.TempDir(), t.TempDir()
	keep := c.Library("keep", "Keep", "book", keepRoot)
	lib := c.Library("r", "Reading", "book", root)
	keepBooks := readingCleanupBooks(t, c, keep, keepRoot, "Keep", 2)
	books := readingCleanupBooks(t, c, lib, root, "Shelf", 5)
	before := readingCleanupCount(t, c, `SELECT sum(publisher_sequence) FROM reading_path_head WHERE item_id IN(?,?)`, keepBooks[0].item, keepBooks[1].item)
	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id='r'`).Scan(&source); err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO reading_path_semantic_pending VALUES(?) ON CONFLICT DO NOTHING`, lib)
	c.Exec(`INSERT INTO reading_path_availability_pending VALUES(?,?) ON CONFLICT DO NOTHING`, books[0].item, lib)
	c.Exec(`INSERT INTO reading_path_source_barrier(source_id,job_id,incarnation,source_generation,root_identity,root,phase) VALUES(?,'job','inc',1,'id',?,'open') ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, source, root)
	c.Exec(`INSERT INTO reading_path_block(job_id,source_id,directory_key,mode,reason) VALUES('job',?,'Shelf','scope','test')`, source)
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(28,?,1) ON CONFLICT DO NOTHING`, lib)
	c.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,attempts,failed_ms) VALUES(28,?,'test',1,1) ON CONFLICT DO NOTHING`, lib)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	readingCleanupDrain(t, c, "r", 400)
	for _, table := range []string{"reading_path_object_keys", "reading_path_dirty_work", "reading_path_availability_pending", "reading_path_semantic_pending"} {
		if got := readingCleanupCount(t, c, `SELECT count(*) FROM `+table+` WHERE library_id=?`, lib); got != 0 {
			t.Fatal(table, got)
		}
	}
	for _, table := range []string{"reading_path_source_barrier", "reading_path_block"} {
		if got := readingCleanupCount(t, c, `SELECT count(*) FROM `+table+` WHERE source_id=?`, source); got != 0 {
			t.Fatal(table, got)
		}
	}
	if readingCleanupCount(t, c, `SELECT count(*) FROM libraries WHERE id='r'`)+readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='r'`) != 0 {
		t.Fatal("final rows remain")
	}
	if readingCleanupCount(t, c, `SELECT count(*) FROM catalog_dirty WHERE domain=28 AND entity_id=?`, lib) != 1 || readingCleanupCount(t, c, `SELECT count(*) FROM catalog_derivation_failures WHERE domain=28 AND entity_id=?`, lib) != 1 {
		t.Fatal("removal touched foreign availability authority")
	}
	if readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_object_keys WHERE library_id=?`, keep) != len(keepBooks) || readingCleanupCount(t, c, `SELECT sum(publisher_sequence) FROM reading_path_head WHERE item_id IN(?,?)`, keepBooks[0].item, keepBooks[1].item) != before {
		t.Fatal("sibling library changed")
	}
}

func TestReadingRemovalCursorIsStrictAndResumable(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	readingCleanupBooks(t, c, lib, root, "Shelf", 8)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	for _, raw := range []string{
		fmt.Sprintf(`{"d":%q,"h":%d,"s":"residue","z":1}`, readingRemovalDigest, lib),
		fmt.Sprintf(`{"d":%q,"h":%d,"s":"residue","c":"{bad"}`, readingRemovalDigest, lib),
		fmt.Sprintf(`{"d":%q,"h":%d,"s":"private"}`, readingRemovalDigest, lib),
		fmt.Sprintf(`{"d":%q,"h":%d,"s":"residue","h":%d}`, readingRemovalDigest, lib, lib),
	} {
		if _, err := readingRemovalLoad(raw); !errors.Is(err, errReadingRemovalCursor) {
			t.Fatal("accepted noncanonical cursor", raw, err)
		}
	}
	var stored string
	for range 200 {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = removeLibraryBatchTx(ctx, tx, "r")
			return err
		})
		if done {
			t.Fatal("finished before storing restart cursor")
		}
		var phase int
		if err := c.DB.QueryRow(`SELECT phase,object_cursor FROM catalog_removal_progress WHERE kind='library' AND target='r'`).Scan(&phase, &stored); err != nil {
			t.Fatal(err)
		}
		if phase == removalLibrary && stored != "" {
			break
		}
		stored = ""
	}
	if stored == "" {
		t.Fatal("no restart cursor")
	}
	state, err := readingRemovalLoad(stored)
	if err != nil || state.Handle != lib {
		t.Fatal(state, err)
	}
	before := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib)
	c.Exec(`UPDATE catalog_removal_progress SET object_cursor=? WHERE kind='library' AND target='r'`, `{"d":"foreign","h":1,"s":"residue"}`)
	err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error { _, err := removeLibraryBatchTx(context.Background(), tx, "r"); return err })
	if !errors.Is(err, errReadingRemovalPlan) || readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib) != before {
		t.Fatal("foreign cursor changed residue", err)
	}
	c.Exec(`UPDATE catalog_removal_progress SET object_cursor=? WHERE kind='library' AND target='r'`, stored)
	readingCleanupDrain(t, c, "r", 200)
	if readingCleanupCount(t, c, `SELECT count(*) FROM catalog_removal_progress WHERE kind='library' AND target='r'`) != 0 {
		t.Fatal("restart left cursor")
	}
}

func TestReadingRemovalRefusesLiveOrForeignLifetime(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	other := c.Library("other", "Other", "book", t.TempDir())
	readingCleanupBooks(t, c, lib, root, "Shelf", 1)
	for _, handle := range []int64{lib, other, lib + 1000} {
		err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			p, err := loadRemovalProgressTx(context.Background(), tx, "library", "r")
			if err != nil {
				return err
			}
			_, err = removeLibraryReadingTx(context.Background(), tx, "r", handle, &p)
			return err
		})
		if !errors.Is(err, errReadingRemovalLifetime) {
			t.Fatal("accepted foreign/live lifetime", handle, err)
		}
	}
	if readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_object_keys WHERE library_id=?`, lib) != 1 {
		t.Fatal("refused lifetime changed per-file witness")
	}
}

func TestReadingRemovalResidueYieldsUntilFinalize(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("gap", "Gap", "book", root)
	// No source row, so the raw source window is empty and this turn is only
	// the library-leading units. Every row below is real residue for the handle.
	c.Exec(`DELETE FROM library_sources WHERE library_id=?`, "gap")
	c.Exec(`DELETE FROM reading_path_dirty_work WHERE library_id=?`, lib)
	c.Exec(`INSERT INTO reading_path_semantic_pending(library_id) VALUES(?1) ON CONFLICT DO NOTHING`, lib)
	const dirty = 5
	for i := 0; i < dirty; i++ {
		c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?1,'object',?2) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, lib, fmt.Sprintf("residue-object-%d", i))
	}
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	// Retirement itself enqueues a library work row. Keep that production
	// trigger effect in the oracle alongside the five explicit object rows.
	dirtyBefore := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib)
	if dirtyBefore < dirty {
		t.Fatalf("fixture lost explicit object work: %d", dirtyBefore)
	}

	// A cursor already at the verify point, exactly where an interrupted verify
	// resumes. The first probe that is not empty rewinds the whole plan.
	verify, err := phasedpurge.EncodeCursor(phasedpurge.Cursor{Unit: len(readingRemovalResidueUnits)})
	if err != nil {
		t.Fatal(err)
	}
	state, err := json.Marshal(readingRemovalState{Digest: readingRemovalDigest, Handle: lib, Stage: readingRemovalResidueStage, Cursor: verify})
	if err != nil {
		t.Fatal(err)
	}
	c.Exec(`INSERT INTO catalog_removal_progress(kind,target,phase,object_cursor) VALUES('library','gap',5,?)
 ON CONFLICT(kind,target) DO UPDATE SET phase=5,object_cursor=excluded.object_cursor`, string(state))

	done := false
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		var err error
		done, err = removeLibraryBatchTx(ctx, tx, "gap")
		return err
	})
	if done {
		t.Fatal("the removal claimed Done before the residue engine finalized")
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM libraries WHERE id='gap'`); n != 1 {
		t.Fatalf("the final library row went before the residue proof: %d", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='gap'`); n != 1 {
		t.Fatalf("the final catalogue row went before the residue proof: %d", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib); n != dirtyBefore {
		t.Fatalf("a verifying turn changed work rows: %d, before %d", n, dirtyBefore)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_semantic_pending WHERE library_id=?`, lib); n != 1 {
		t.Fatalf("a verifying turn deleted the semantic row: %d", n)
	}

	readingCleanupDrain(t, c, "gap", 80)
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib); n != 0 {
		t.Fatalf("the finished removal left %d work rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_semantic_pending WHERE library_id=?`, lib); n != 0 {
		t.Fatalf("the finished removal left %d semantic rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM libraries WHERE id='gap'`); n != 0 {
		t.Fatalf("the finished removal left %d library rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='gap'`); n != 0 {
		t.Fatalf("the finished removal left %d catalogue rows", n)
	}
}
