package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// Source-only tests for the phase-5 Reading lifetime owner. They drive
// removeLibraryBatchTx, the real removal seam, and read the rows back. They do
// not start the Reading dispatcher and do not claim anything about it: the
// point of the owner is that a retired library's private lifetime does not need
// it.

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

// A retired library's private lifetime is reclaimed by the removal itself:
// the activated overlay build, the parked/queued-0 scope and the settled scope
// all reach the same empty fixed point, and the library-leading residue its own
// deletes requeue is gone before the removal reports finished. A live sibling
// library keeps its scope, build, children, slot counters and public rows; the
// domain28 rows are neither consulted nor deleted.
func TestReadingRemovalOwnerDrainsRetiredLifetimeAndResidue(t *testing.T) {
	c := catalogtest.Open(t)
	keepRoot, root := t.TempDir(), t.TempDir()
	keep := c.Library("keep", "Keep", "book", keepRoot)
	lib := c.Library("r", "Reading", "book", root)

	keepBooks := readingPurgeBooks(t, c, keep, keepRoot, "Keep", 1)
	keepScope := readingPurgeScope(t, c, keep, "Keep")
	keepBuild := readingPurgeBuild(t, c, keepScope, "flatten", keepBooks)
	keepState := readingPurgeScopeState(t, c, keepScope)
	keepCounts := readingPurgeScopeCounts(t, c, keepScope)
	keepChildren := readingPurgeChildRows(t, c, keepBuild)

	shelfBooks := readingPurgeBooks(t, c, lib, root, "Shelf", 2)
	shelfScope := readingPurgeScope(t, c, lib, "Shelf")
	shelfBuild := readingPurgeBuild(t, c, shelfScope, "flatten", shelfBooks)

	boxBooks := readingPurgeBooks(t, c, lib, root, "Box", 1)
	boxScope := readingPurgeScope(t, c, lib, "Box")
	boxBuild := readingPurgeBuild(t, c, boxScope, "capture", boxBooks)

	settledBooks := readingPurgeBooks(t, c, lib, root, "Settled", 1)
	settledScope := readingPurgeScope(t, c, lib, "Settled")

	var source string
	if err := c.DB.QueryRow(`SELECT id FROM library_sources WHERE library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, lib).Scan(&source); err != nil {
		t.Fatal(err)
	}

	// Activated: the singleton slot names the retired scope's own build, with
	// the overlay flag on the same build.
	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, shelfBuild)
	// Settled: nothing runnable, only its private rows.
	c.Exec(`UPDATE reading_path_scope SET queued=0,parked=0,completed=desired WHERE id=?`, settledScope)

	// Library-leading residue, and the source-scoped rows the removal's own
	// UPDATE/DELETE triggers requeue.
	c.Exec(`INSERT INTO reading_path_inputs(library_id,revision) VALUES(?1,1) ON CONFLICT(library_id) DO UPDATE SET revision=revision+1`, lib)
	c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?1,'library',CAST(?1 AS TEXT)) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, lib)
	c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?1,'object',?2) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, lib, shelfBooks[0].object)
	c.Exec(`INSERT INTO reading_path_dirty_work(library_id,kind,work_key) VALUES(?1,'item',CAST(?2 AS TEXT)) ON CONFLICT(library_id,kind,work_key) DO UPDATE SET requested=requested+1`, lib, settledBooks[0].item)
	c.Exec(`INSERT INTO reading_path_availability_pending(entity_id,library_id) VALUES(?1,?2) ON CONFLICT(entity_id) DO UPDATE SET library_id=excluded.library_id`, shelfBooks[0].item, lib)
	c.Exec(`INSERT INTO reading_path_source_barrier(source_id,job_id,incarnation,source_generation,root_identity,root,coverage_revision,after_issue,phase,reason) VALUES(?1,'removal-job','inc',1,'id',?2,0,0,'open','') ON CONFLICT(source_id) DO UPDATE SET job_id=excluded.job_id`, source, root)
	c.Exec(`INSERT INTO reading_path_block(job_id,source_id,directory_key,mode,reason) VALUES('removal-job',?1,'Shelf','scope','removal-test') ON CONFLICT(source_id,job_id,directory_key,mode) DO NOTHING`, source)

	// Parked LAST, so no later fixture write unparks it: the dispatcher would
	// never select it, which is exactly why the removal owns the scope.
	c.Exec(`UPDATE reading_path_scope SET queued=0,parked=1 WHERE id=?`, boxScope)
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(28,?1,1) ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, lib)
	c.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,attempts,failed_ms) VALUES(28,?1,'removal-test',1,1) ON CONFLICT(domain,entity_id) DO UPDATE SET attempts=attempts+1`, lib)

	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)

	readingCleanupDrain(t, c, "r", 400)

	for _, scope := range []int64{shelfScope, boxScope, settledScope} {
		if got := readingPurgeScopeCounts(t, c, scope); got != (readingPurgeCounts{}) {
			t.Fatalf("scope %d left builds=%d keys=%d entities=%d scopes=%d", scope, got.builds, got.keys, got.entities, got.scopes)
		}
	}
	for _, build := range []int64{shelfBuild, boxBuild} {
		if n := readingPurgeChildRows(t, c, build); n != 0 {
			t.Fatalf("build %d kept %d children", build, n)
		}
	}

	for _, query := range []struct {
		name  string
		sql   string
		value any
	}{
		{"inputs", `SELECT count(*) FROM reading_path_inputs WHERE library_id=?`, lib},
		{"work", `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib},
		{"availability", `SELECT count(*) FROM reading_path_availability_pending WHERE library_id=?`, lib},
		{"semantic", `SELECT count(*) FROM reading_path_semantic_pending WHERE library_id=?`, lib},
		{"barrier", `SELECT count(*) FROM reading_path_source_barrier WHERE source_id=?`, source},
		{"blocks", `SELECT count(*) FROM reading_path_block WHERE source_id=?`, source},
		{"scopes", `SELECT count(*) FROM reading_path_scope WHERE library_id=?`, lib},
		{"progress", `SELECT count(*) FROM catalog_removal_progress WHERE kind='library' AND target=?`, "r"},
	} {
		if n := readingCleanupCount(t, c, query.sql, query.value); n != 0 {
			t.Fatalf("orphan %s=%d", query.name, n)
		}
	}
	for _, id := range []int64{shelfBooks[0].item, boxBooks[0].item, settledBooks[0].item} {
		if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_head WHERE item_id=?`, id); n != 0 {
			t.Fatalf("item %d kept %d head rows", id, n)
		}
	}
	var slotBuild, slotOverlay sql.NullInt64
	if err := c.DB.QueryRow(`SELECT build_id,overlay_id FROM reading_path_slot WHERE singleton=1`).Scan(&slotBuild, &slotOverlay); err != nil {
		t.Fatal(err)
	}
	if slotBuild.Valid || slotOverlay.Valid {
		t.Fatalf("the retired build stayed in the slot: build=%v overlay=%v", slotBuild, slotOverlay)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='r'`); n != 0 {
		t.Fatalf("the retired catalogue row stayed: %d", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM libraries WHERE id='r'`); n != 0 {
		t.Fatalf("the retired library row stayed: %d", n)
	}
	// Domain28 work belongs to the removal's public authority, not to private
	// lifetime reclamation: it is neither consulted nor deleted here.
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_dirty WHERE domain=28 AND entity_id=?`, lib); n != 1 {
		t.Fatalf("the domain28 dirty row was touched: %d", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_derivation_failures WHERE domain=28 AND entity_id=?`, lib); n != 1 {
		t.Fatalf("the domain28 failure was touched: %d", n)
	}

	if got := readingPurgeScopeState(t, c, keepScope); got != keepState {
		t.Fatalf("the live sibling scope changed: %s, was %s", got, keepState)
	}
	if got := readingPurgeScopeCounts(t, c, keepScope); got != keepCounts {
		t.Fatalf("the live sibling scope lost rows: %+v, was %+v", got, keepCounts)
	}
	if readingPurgeChildRows(t, c, keepBuild) != keepChildren {
		t.Fatal("the live sibling build lost a child")
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='keep' AND retired=0`); n != 1 {
		t.Fatal("the live sibling library row changed")
	}
}

// A phase-5 page is a durable point: the state lives in
// catalog_removal_progress.object_cursor, so a restart reopens with only that
// string. A state from another plan, a non-canonical state and a malformed
// inner cursor are refused before any row changes, and a refusal never resets
// the stored state.
func TestReadingRemovalCursorIsStrictAndResumable(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	books := readingPurgeBooks(t, c, lib, root, "Shelf", 2)
	scope := readingPurgeScope(t, c, lib, "Shelf")
	build := readingPurgeBuild(t, c, scope, "flatten", books)
	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, build)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)

	if _, err := readingRemovalLoad(`{"d":"another-plan","h":1,"s":"private"}`); !errors.Is(err, errReadingRemovalPlan) {
		t.Fatalf("a state from another plan was accepted: %v", err)
	}
	if _, err := readingRemovalLoad(fmt.Sprintf(`{"d":%q,"s":"private"}`, readingRemovalDigest)); !errors.Is(err, errReadingRemovalCursor) {
		t.Fatalf("a state without a handle was accepted: %v", err)
	}
	if _, err := readingRemovalLoad(fmt.Sprintf(`{"d":%q,"h":1,"s":"private","z":1}`, readingRemovalDigest)); !errors.Is(err, errReadingRemovalCursor) {
		t.Fatalf("a state with an unknown field was accepted: %v", err)
	}
	canonical, err := json.Marshal(readingRemovalState{Digest: readingRemovalDigest, Handle: lib, Stage: readingRemovalPrivateStage})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := readingRemovalLoad(string(canonical))
	if err != nil || loaded.Stage != readingRemovalPrivateStage || loaded.Handle != lib {
		t.Fatalf("the canonical state was refused: %+v %v", loaded, err)
	}
	badCursor := fmt.Sprintf(`{"d":%q,"h":%d,"s":"residue","c":"{bad"}`, readingRemovalDigest, lib)
	if _, err := readingRemovalLoad(badCursor); !errors.Is(err, errReadingRemovalCursor) {
		t.Fatalf("a malformed residue cursor was accepted: %v", err)
	}

	// Drive the real removal until phase 5 has stored its own state.
	var stored string
	for i := 0; i < 80; i++ {
		done := false
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			var err error
			done, err = removeLibraryBatchTx(ctx, tx, "r")
			return err
		})
		if done {
			t.Fatal("the removal finished before a phase-5 cursor was stored")
		}
		var phase int
		var cursor string
		if err := c.DB.QueryRow(`SELECT phase,object_cursor FROM catalog_removal_progress WHERE kind='library' AND target='r'`).Scan(&phase, &cursor); err != nil {
			t.Fatal(err)
		}
		if phase == removalLibrary && cursor != "" {
			stored = cursor
			break
		}
	}
	if stored == "" {
		t.Fatal("phase 5 stored no Reading state")
	}
	reopened, err := readingRemovalLoad(stored)
	if err != nil {
		t.Fatalf("the restart state was refused: %v", err)
	}
	if reopened.Handle != lib || reopened.Stage != readingRemovalPrivateStage || reopened.Scope != scope || reopened.ScopeCursor == "" {
		t.Fatalf("the restart state lost its scope: %+v", reopened)
	}

	// A stored state that is not this plan's is a visible refusal, not a reset,
	// and the refused turn changes no child row.
	c.Exec(`UPDATE catalog_removal_progress SET object_cursor=? WHERE kind='library' AND target='r'`, `{"d":"another-plan","h":1,"s":"private"}`)
	before := readingPurgeChildRows(t, c, build)
	err = dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		_, e := removeLibraryBatchTx(context.Background(), tx, "r")
		return e
	})
	if !errors.Is(err, errReadingRemovalPlan) {
		t.Fatalf("a foreign stored state was not refused: %v", err)
	}
	if after := readingPurgeChildRows(t, c, build); after != before {
		t.Fatalf("a refused turn changed children: %d, was %d", after, before)
	}
	c.Exec(`UPDATE catalog_removal_progress SET object_cursor=? WHERE kind='library' AND target='r'`, stored)

	// The restart is only the stored string: continue to the same fixed point.
	readingCleanupDrain(t, c, "r", 200)
	if got := readingPurgeScopeCounts(t, c, scope); got != (readingPurgeCounts{}) {
		t.Fatalf("the restarted removal left builds=%d keys=%d entities=%d scopes=%d", got.builds, got.keys, got.entities, got.scopes)
	}
	if n := readingPurgeChildRows(t, c, build); n != 0 {
		t.Fatalf("the restarted removal left %d children", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_removal_progress WHERE kind='library' AND target='r'`); n != 0 {
		t.Fatalf("the finished removal left %d progress rows", n)
	}
}

// The lifetime guard is the same-writer retirement proof: a live library and a
// handle that is not the library's are both refused before a row changes.
func TestReadingRemovalRefusesLiveOrForeignLifetime(t *testing.T) {
	c := catalogtest.Open(t)
	root, otherRoot := t.TempDir(), t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	other := c.Library("other", "Other", "book", otherRoot)
	readingPurgeBooks(t, c, lib, root, "Shelf", 1)
	scope := readingPurgeScope(t, c, lib, "Shelf")

	for _, handle := range []int64{lib, other, lib + 1000} {
		expected := ErrReadingScopePurgeLifetime
		err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
			p, e := loadRemovalProgressTx(context.Background(), tx, "library", "r")
			if e != nil {
				return e
			}
			_, e = removeLibraryReadingTx(context.Background(), tx, "r", handle, &p)
			return e
		})
		if !errors.Is(err, expected) {
			t.Fatalf("handle %d was accepted: %v", handle, err)
		}
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_scope WHERE id=?`, scope); n != 1 {
		t.Fatalf("a refused lifetime changed the scope: %d", n)
	}
}

// The residue engine is the only authority for its completion. A turn that
// saves a position, or an interrupted/re-wound verify, yields: it must not
// advance the stage, remove the final rows, or report Done. This reproduces the
// rewind the engine performs when a verify probe finds one dirty-work row.
func TestReadingRemovalResidueYieldsUntilFinalize(t *testing.T) {
	c := catalogtest.Open(t)
	root := t.TempDir()
	lib := c.Library("gap", "Gap", "book", root)
	// No source row, so the raw source window is empty and this turn is only
	// the library-leading units. Every row below is real residue for the handle.
	c.Exec(`DELETE FROM library_sources WHERE library_id=?`, "gap")
	c.Exec(`DELETE FROM reading_path_dirty_work WHERE library_id=?`, lib)
	c.Exec(`INSERT INTO reading_path_inputs(library_id,revision) VALUES(?1,1) ON CONFLICT(library_id) DO UPDATE SET revision=revision+1`, lib)
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
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_inputs WHERE library_id=?`, lib); n != 1 {
		t.Fatalf("a verifying turn deleted the inputs row: %d", n)
	}

	readingCleanupDrain(t, c, "gap", 80)
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_dirty_work WHERE library_id=?`, lib); n != 0 {
		t.Fatalf("the finished removal left %d work rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM reading_path_inputs WHERE library_id=?`, lib); n != 0 {
		t.Fatalf("the finished removal left %d inputs rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM libraries WHERE id='gap'`); n != 0 {
		t.Fatalf("the finished removal left %d library rows", n)
	}
	if n := readingCleanupCount(t, c, `SELECT count(*) FROM catalog_libraries WHERE library_id='gap'`); n != 0 {
		t.Fatalf("the finished removal left %d catalogue rows", n)
	}
}

// The singleton slot is global. A retired scope's drain may clear only the
// columns that name one of its own builds; another library's published build
// stays, and a column the scope does not own is never cleared with it. (The
// slot CHECK makes a truly mixed row unrepresentable, so both branches of the
// detach predicate are covered instead.)
func TestReadingRemovalPreservesSiblingSlotColumn(t *testing.T) {
	c := catalogtest.Open(t)
	keepRoot, root := t.TempDir(), t.TempDir()
	keep := c.Library("keep", "Keep", "book", keepRoot)
	lib := c.Library("r", "Reading", "book", root)

	keepBooks := readingPurgeBooks(t, c, keep, keepRoot, "Keep", 1)
	keepScope := readingPurgeScope(t, c, keep, "Keep")
	keepBuild := readingPurgeBuild(t, c, keepScope, "flatten", keepBooks)
	keepCounts := readingPurgeScopeCounts(t, c, keepScope)
	keepChildren := readingPurgeChildRows(t, c, keepBuild)

	mine := readingPurgeBooks(t, c, lib, root, "Shelf", 1)
	mineScope := readingPurgeScope(t, c, lib, "Shelf")
	mineBuild := readingPurgeBuild(t, c, mineScope, "capture", mine)

	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, keepBuild)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	readingPurgeDrain(t, c, mineScope, lib)

	var slotBuild, slotOverlay sql.NullInt64
	if err := c.DB.QueryRow(`SELECT build_id,overlay_id FROM reading_path_slot WHERE singleton=1`).Scan(&slotBuild, &slotOverlay); err != nil {
		t.Fatal(err)
	}
	if !slotBuild.Valid || slotBuild.Int64 != keepBuild || !slotOverlay.Valid || slotOverlay.Int64 != keepBuild {
		t.Fatalf("the sibling slot was changed: build=%v overlay=%v, want %d", slotBuild, slotOverlay, keepBuild)
	}
	if got := readingPurgeScopeCounts(t, c, mineScope); got != (readingPurgeCounts{}) {
		t.Fatalf("the retired scope left builds=%d keys=%d entities=%d scopes=%d", got.builds, got.keys, got.entities, got.scopes)
	}
	if readingPurgeChildRows(t, c, mineBuild) != 0 {
		t.Fatal("the retired build kept children")
	}
	if got := readingPurgeScopeCounts(t, c, keepScope); got != keepCounts {
		t.Fatalf("the sibling scope lost rows: %+v, was %+v", got, keepCounts)
	}
	if readingPurgeChildRows(t, c, keepBuild) != keepChildren {
		t.Fatal("the sibling build lost a child")
	}

	// The other branch: a slot that names this library's own build is cleared.
	otherDir := readingPurgeBooks(t, c, lib, root, "Box", 1)
	otherScope := readingPurgeScope(t, c, lib, "Box")
	otherBuild := readingPurgeBuild(t, c, otherScope, "flatten", otherDir)
	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, otherBuild)
	readingPurgeDrain(t, c, otherScope, lib)
	if err := c.DB.QueryRow(`SELECT build_id,overlay_id FROM reading_path_slot WHERE singleton=1`).Scan(&slotBuild, &slotOverlay); err != nil {
		t.Fatal(err)
	}
	if slotBuild.Valid || slotOverlay.Valid {
		t.Fatalf("the retired build stayed in the slot: build=%v overlay=%v", slotBuild, slotOverlay)
	}
}
