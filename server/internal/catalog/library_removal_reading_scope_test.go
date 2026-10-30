package catalog

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/phasedpurge"
)

// Source-only tests for RetireReadingScopePage. They build real baseline rows
// through the catalogue write API and real reading_path rows, then drive the
// new page function directly. They are not a claim that the full library
// removal is wired to it; the root integration owns that call site.

type readingPurgeBook struct {
	item, asset int64
	object      string
}

type readingPurgeCounts struct {
	builds, keys, entities, scopes int
}

func readingPurgeBooks(t *testing.T, c *catalogtest.Catalog, library int64, root, directory string, n int) []readingPurgeBook {
	t.Helper()
	var source, incarnation string
	if err := c.DB.QueryRow(`SELECT id,incarnation FROM library_sources WHERE library_id=(SELECT library_id FROM catalog_libraries WHERE id=?)`, library).Scan(&source, &incarnation); err != nil {
		t.Fatal(err)
	}
	books := make([]readingPurgeBook, 0, n)
	for i := 0; i < n; i++ {
		item := c.Entity(compactcatalog.Entity{
			Library: library,
			Kind:    compactcatalog.Ebook,
			Key:     fmt.Sprintf("ebook:%s:%d", directory, i),
			Title:   fmt.Sprintf("Book %s %d", directory, i),
		}, nil)
		asset, token := c.File(item.ID, filepath.Join(root, directory, fmt.Sprintf("Book %d.epub", i)), 0)
		object := fmt.Sprintf("purge-%s-%d", directory, i)
		relative := fmt.Sprintf("%s/Book %d.epub", directory, i)
		c.Exec(`INSERT INTO reading_publications(item_id,asset_id,format) VALUES(?,?,'epub')`, item.ID, asset)
		c.Exec(`INSERT INTO inventory_objects(id,source_id,asset_id,root_incarnation,relative_path,revision,evidence_json,size,modified_ns) VALUES(?,?,?,?,?,'r','{}',1,1)`,
			object, source, token, incarnation, relative)
		c.Write(func(ctx context.Context, tx *sql.Tx) error {
			return compactcatalog.ReadingNormalizeObjectTx(ctx, tx, object)
		})
		books = append(books, readingPurgeBook{item: item.ID, asset: asset, object: object})
	}
	return books
}

func readingPurgeScope(t *testing.T, c *catalogtest.Catalog, handle int64, directory string) int64 {
	t.Helper()
	var id int64
	if err := c.DB.QueryRow(`SELECT id FROM reading_path_scope WHERE library_id=? AND directory_key=? ORDER BY id DESC LIMIT 1`, handle, directory).Scan(&id); err != nil {
		t.Fatalf("scope %s: %v", directory, err)
	}
	return id
}

// readingPurgeBuild inserts one real build and one capture, representative and
// stage row per book. It is fixture data, not a second teardown path.
func readingPurgeBuild(t *testing.T, c *catalogtest.Catalog, scope int64, phase string, books []readingPurgeBook) int64 {
	t.Helper()
	var id int64
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		// A slot overlay is an already published flatten build, not a ready or
		// cleanup build. Seed the same valid counter/phase relationship enforced
		// by the installed overlay guard; no guard is dropped for this fixture.
		var published sql.NullInt64
		if phase == "flatten" {
			if e := tx.QueryRowContext(ctx, `UPDATE reading_path_clock SET sequence=sequence+1 WHERE singleton=1 RETURNING sequence`).Scan(&published); e != nil {
				return e
			}
		}
		if err := tx.QueryRowContext(ctx, `INSERT INTO reading_path_build(scope_id,desired,input_revision,source_generation,root_identity,job_id,phase,publish_sequence) VALUES(?1,1,1,1,'','purge',?2,?3) RETURNING id`, scope, phase, published).Scan(&id); err != nil {
			return err
		}
		for i := range books {
			b := books[i]
			relative := fmt.Sprintf("Book %d.epub", i)
			if _, err := tx.ExecContext(ctx, `INSERT INTO reading_path_capture(build_id,item_id,asset_id,object_id,source_revision,relative_path,item_kind,series_evidence) VALUES(?1,?2,?3,?4,'r',?5,20,0)`,
				id, b.item, b.asset, b.object, relative); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO reading_path_representative(build_id,item_id,asset_id,series_evidence) VALUES(?1,?2,?3,0)`,
				id, b.item, b.asset); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO reading_path_stage(build_id,item_id,asset_id,object_id,source_revision) VALUES(?1,?2,?3,?4,'r')`,
				id, b.item, b.asset, b.object); err != nil {
				return err
			}
		}
		return nil
	})
	return id
}

func readingPurgePage(t *testing.T, c *catalogtest.Catalog, scope, handle int64, cursor string) (ReadingScopePurgeResult, error) {
	t.Helper()
	var out ReadingScopePurgeResult
	var saved string
	err := dbwork.WithWriteTx(context.Background(), c.DB, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
		var e error
		out, e = RetireReadingScopePage(context.Background(), tx, ReadingScopePurgeRequest{
			ScopeID:       scope,
			LibraryHandle: handle,
			StoredCursor:  cursor,
			Save: func(_ context.Context, _ *sql.Tx, stored string) error {
				saved = stored
				return nil
			},
		})
		return e
	})
	if err == nil && saved != "" {
		out.StoredCursor = saved
	}
	return out, err
}

func readingPurgeDrain(t *testing.T, c *catalogtest.Catalog, scope, handle int64) {
	t.Helper()
	cursor := ""
	for i := 0; i < 100000; i++ {
		out, err := readingPurgePage(t, c, scope, handle, cursor)
		if err != nil {
			t.Fatalf("scope %d page %d: %v", scope, i, err)
		}
		if out.Reads > phasedpurge.ReadLimit {
			t.Fatalf("scope %d page %d read %d, past the shared %d", scope, i, out.Reads, phasedpurge.ReadLimit)
		}
		if out.Effects > phasedpurge.EffectLimit {
			t.Fatalf("scope %d page %d changed %d, past the shared %d", scope, i, out.Effects, phasedpurge.EffectLimit)
		}
		if out.Done {
			return
		}
		if !out.Worked {
			t.Fatalf("scope %d page %d made no progress", scope, i)
		}
		cursor = out.StoredCursor
	}
	t.Fatalf("scope %d did not finish", scope)
}

func readingPurgeScopeCounts(t *testing.T, c *catalogtest.Catalog, scope int64) readingPurgeCounts {
	t.Helper()
	var out readingPurgeCounts
	if err := c.DB.QueryRow(`SELECT (SELECT count(*) FROM reading_path_build WHERE scope_id=?1),
 (SELECT count(*) FROM reading_path_object_keys WHERE scope_id=?1),
 (SELECT count(*) FROM reading_path_scope_entities WHERE scope_id=?1),
 (SELECT count(*) FROM reading_path_scope WHERE id=?1)`, scope).Scan(&out.builds, &out.keys, &out.entities, &out.scopes); err != nil {
		t.Fatal(err)
	}
	return out
}

func readingPurgeChildRows(t *testing.T, c *catalogtest.Catalog, build int64) int {
	t.Helper()
	return rows(t, c, `SELECT (SELECT count(*) FROM reading_path_capture WHERE build_id=?1)
 +(SELECT count(*) FROM reading_path_representative WHERE build_id=?1)
 +(SELECT count(*) FROM reading_path_stage WHERE build_id=?1)`, build)
}

func readingPurgeScopeState(t *testing.T, c *catalogtest.Catalog, scope int64) string {
	t.Helper()
	var desired, completed, queued, served, pending, parked, closure int64
	if err := c.DB.QueryRow(`SELECT desired,completed,queued,served,pending,parked,closure_seal FROM reading_path_scope WHERE id=?`, scope).Scan(&desired, &completed, &queued, &served, &pending, &parked, &closure); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%d/%d/%d/%d/%d/%d/%d", desired, completed, queued, served, pending, parked, closure)
}

// A retired library can own an activated scope (slot + overlay + children), a
// parked scope behind domain28 work, and a settled scope with no build. Every
// one must reach the same empty fixed point without the Reading dispatcher, and
// a sibling library's scope, build and counters must be untouched.
func TestReadingScopePurgeDrainsActivatedParkedAndSettledScopes(t *testing.T) {
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

	readingPurgeBooks(t, c, lib, root, "Settled", 1)
	settledScope := readingPurgeScope(t, c, lib, "Settled")

	// Activated: the singleton slot names this scope's build, with the overlay
	// flag set to the same build.
	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, shelfBuild)
	// Parked: no dispatcher will ever select it, and a domain28 failure says so.
	c.Exec(`UPDATE reading_path_scope SET queued=0,parked=1 WHERE id=?`, boxScope)
	c.Exec(`INSERT INTO catalog_dirty(domain,entity_id,revision) VALUES(28,?1,1) ON CONFLICT(domain,entity_id) DO UPDATE SET revision=revision+1`, lib)
	c.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,attempts,failed_ms) VALUES(28,?1,'purge-test',1,1) ON CONFLICT(domain,entity_id) DO UPDATE SET attempts=attempts+1`, lib)
	// Settled: no build and nothing runnable, only private keys/entities.
	c.Exec(`UPDATE reading_path_scope SET queued=0,parked=0,completed=desired WHERE id=?`, settledScope)

	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)

	for _, scope := range []int64{shelfScope, boxScope, settledScope} {
		readingPurgeDrain(t, c, scope, lib)
	}

	for _, check := range []struct {
		scope int64
		build int64
	}{
		{shelfScope, shelfBuild},
		{boxScope, boxBuild},
		{settledScope, 0},
	} {
		if got := readingPurgeScopeCounts(t, c, check.scope); got != (readingPurgeCounts{}) {
			t.Fatalf("scope %d left builds=%d keys=%d entities=%d scopes=%d", check.scope, got.builds, got.keys, got.entities, got.scopes)
		}
		if check.build != 0 && readingPurgeChildRows(t, c, check.build) != 0 {
			t.Fatalf("build %d kept children", check.build)
		}
	}

	var slotBuild, slotOverlay sql.NullInt64
	if err := c.DB.QueryRow(`SELECT build_id,overlay_id FROM reading_path_slot WHERE singleton=1`).Scan(&slotBuild, &slotOverlay); err != nil {
		t.Fatal(err)
	}
	if slotBuild.Valid || slotOverlay.Valid {
		t.Fatalf("the retired scope's slot was not detached: build=%v overlay=%v", slotBuild, slotOverlay)
	}

	if got := readingPurgeScopeState(t, c, keepScope); got != keepState {
		t.Fatalf("the sibling scope changed: %s, was %s", got, keepState)
	}
	if got := readingPurgeScopeCounts(t, c, keepScope); got != keepCounts {
		t.Fatalf("the sibling scope lost rows: %+v, was %+v", got, keepCounts)
	}
	if readingPurgeChildRows(t, c, keepBuild) != keepChildren {
		t.Fatal("the sibling build lost a child")
	}
	// Domain28 work belongs to the library/source removal owner, not this scope
	// teardown; it is neither consulted nor deleted here.
	if n := rows(t, c, `SELECT count(*) FROM catalog_dirty WHERE domain=28 AND entity_id=?`, lib); n != 1 {
		t.Fatalf("the domain28 dirty row was touched: %d", n)
	}
	if n := rows(t, c, `SELECT count(*) FROM catalog_derivation_failures WHERE domain=28 AND entity_id=?`, lib); n != 1 {
		t.Fatalf("the domain28 failure was touched: %d", n)
	}
}

// The singleton slot is global. Retiring one library's scope must leave another
// library's build in it, together with that library's scope, counters and
// children.
func TestReadingScopePurgePreservesAnotherLibrariesSlotAndCounters(t *testing.T) {
	c := catalogtest.Open(t)
	keepRoot, root := t.TempDir(), t.TempDir()
	keep := c.Library("keep", "Keep", "book", keepRoot)
	lib := c.Library("r", "Reading", "book", root)

	keepBooks := readingPurgeBooks(t, c, keep, keepRoot, "Keep", 1)
	keepScope := readingPurgeScope(t, c, keep, "Keep")
	keepBuild := readingPurgeBuild(t, c, keepScope, "flatten", keepBooks)

	books := readingPurgeBooks(t, c, lib, root, "Shelf", 1)
	scope := readingPurgeScope(t, c, lib, "Shelf")
	readingPurgeBuild(t, c, scope, "cleanup", books)

	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, keepBuild)
	keepState := readingPurgeScopeState(t, c, keepScope)
	keepCounts := readingPurgeScopeCounts(t, c, keepScope)
	keepChildren := readingPurgeChildRows(t, c, keepBuild)
	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)

	readingPurgeDrain(t, c, scope, lib)

	var slotBuild, slotOverlay sql.NullInt64
	if err := c.DB.QueryRow(`SELECT build_id,overlay_id FROM reading_path_slot WHERE singleton=1`).Scan(&slotBuild, &slotOverlay); err != nil {
		t.Fatal(err)
	}
	if !slotBuild.Valid || slotBuild.Int64 != keepBuild || !slotOverlay.Valid || slotOverlay.Int64 != keepBuild {
		t.Fatalf("another library's slot changed: build=%v overlay=%v, want %d", slotBuild, slotOverlay, keepBuild)
	}
	if got := readingPurgeScopeState(t, c, keepScope); got != keepState {
		t.Fatalf("the sibling scope changed: %s, was %s", got, keepState)
	}
	if got := readingPurgeScopeCounts(t, c, keepScope); got != keepCounts {
		t.Fatalf("the sibling scope lost rows: %+v, was %+v", got, keepCounts)
	}
	if readingPurgeChildRows(t, c, keepBuild) != keepChildren {
		t.Fatal("the sibling build lost a child")
	}
}

// A page is a durable point: the caller reopens with only the stored string.
// The first page detaches the slot and leaves the build for later pages; every
// later page selects at most one raw capture/representative/stage row. A
// cursor from another plan, a malformed cursor, and the wrong library lifetime
// are refused before any row changes.
func TestReadingScopePurgeResumesStoredCursorAndRejectsOtherPlans(t *testing.T) {
	c := catalogtest.Open(t)
	root, otherRoot := t.TempDir(), t.TempDir()
	lib := c.Library("r", "Reading", "book", root)
	c.Library("other", "Other", "book", otherRoot)

	books := readingPurgeBooks(t, c, lib, root, "Shelf", 2)
	scope := readingPurgeScope(t, c, lib, "Shelf")
	build := readingPurgeBuild(t, c, scope, "flatten", books)
	c.Exec(`UPDATE reading_path_slot SET build_id=?1,overlay_id=?1 WHERE singleton=1`, build)

	if _, err := readingPurgePage(t, c, scope, 999999, ""); !errors.Is(err, ErrReadingScopePurgeLifetime) {
		t.Fatalf("another handle was accepted: %v", err)
	}
	if _, err := readingPurgePage(t, c, scope, lib, ""); !errors.Is(err, ErrReadingScopePurgeLifetime) {
		t.Fatalf("a live library was accepted: %v", err)
	}
	before := readingPurgeChildRows(t, c, build)
	if _, err := readingPurgePage(t, c, scope, lib, `{"d":"not-the-plan","c":""}`); !errors.Is(err, ErrReadingScopePurgePlan) {
		t.Fatalf("a cursor from another plan was accepted: %v", err)
	}
	bad := fmt.Sprintf(`{"d":%q,"c":"{bad"}`, ReadingScopePurgePlanDigest())
	if _, err := readingPurgePage(t, c, scope, lib, bad); !errors.Is(err, ErrReadingScopePurgeCursor) {
		t.Fatalf("a malformed cursor was accepted: %v", err)
	}
	if after := readingPurgeChildRows(t, c, build); after != before {
		t.Fatalf("a refused page changed children: %d, was %d", after, before)
	}

	c.Exec(`UPDATE catalog_libraries SET retired=1 WHERE id=?`, lib)
	first, err := readingPurgePage(t, c, scope, lib, "")
	if err != nil {
		t.Fatal(err)
	}
	if first.Done || !first.Worked {
		t.Fatalf("the first page did not stop after the slot: done=%v worked=%v", first.Done, first.Worked)
	}
	if first.StoredCursor == "" {
		t.Fatal("the first page returned no durable cursor")
	}
	if first.Reads > phasedpurge.ReadLimit || first.Effects > phasedpurge.EffectLimit {
		t.Fatalf("the first page exceeded the shared meters: reads=%d effects=%d", first.Reads, first.Effects)
	}
	if got := readingPurgeChildRows(t, c, build); got != before {
		t.Fatalf("the slot page also changed children: %d, was %d", got, before)
	}

	// Reopen from only the stored string; no Go state survives.
	cursor := first.StoredCursor
	done := false
	for i := 0; i < 100000; i++ {
		pageBefore := readingPurgeChildRows(t, c, build)
		out, err := readingPurgePage(t, c, scope, lib, cursor)
		if err != nil {
			t.Fatalf("reopen page %d: %v", i, err)
		}
		if out.Reads > phasedpurge.ReadLimit || out.Effects > phasedpurge.EffectLimit {
			t.Fatalf("reopen page %d exceeded the shared meters: reads=%d effects=%d", i, out.Reads, out.Effects)
		}
		if drop := pageBefore - readingPurgeChildRows(t, c, build); drop > 1 {
			t.Fatalf("reopen page %d selected %d raw children", i, drop)
		}
		cursor = out.StoredCursor
		if out.Done {
			done = true
			break
		}
		if !out.Worked {
			t.Fatalf("reopen page %d made no progress", i)
		}
	}
	if !done {
		t.Fatal("the reopened scope purge did not finish")
	}
	if got := readingPurgeScopeCounts(t, c, scope); got != (readingPurgeCounts{}) {
		t.Fatalf("the reopened scope left builds=%d keys=%d entities=%d scopes=%d", got.builds, got.keys, got.entities, got.scopes)
	}
	if readingPurgeChildRows(t, c, build) != 0 {
		t.Fatal("the reopened scope left build children")
	}
}
