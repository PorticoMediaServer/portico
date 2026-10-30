package catalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// vfClass is the frontier state of one class row.
type vfClass struct {
	id, libraryID, generation int64
	sourceTarget, captured    int64
	sourceRevision, revision  int64
}

func vfClassState(t *testing.T, db *sql.DB, classKey string) vfClass {
	t.Helper()
	var state vfClass
	if err := db.QueryRow(`SELECT id,library_id,active_generation,source_target,captured_sequence,source_revision,catalog_revision
	 FROM compact_visibility_classes WHERE class_key=?`, classKey).Scan(&state.id, &state.libraryID, &state.generation,
		&state.sourceTarget, &state.captured, &state.sourceRevision, &state.revision); err != nil {
		t.Fatal(err)
	}
	return state
}

func vfCount(t *testing.T, db *sql.DB, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// vfSourceSequence is the accepted-source clock a class frontier is measured
// against.
func vfSourceSequence(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	sequence, err := compactcatalog.VisibilitySourceClock(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return sequence
}

func vfLibraryRevision(t *testing.T, db *sql.DB, library string) int64 {
	t.Helper()
	return vfCount(t, db, `SELECT revision FROM library_revisions WHERE library_id=?`, library)
}

// vfCaptureTarget runs the production capture inside its own write
// transaction, as both maintenance paths do, and returns what it captured.
func vfCaptureTarget(t *testing.T, db *sql.DB, classID int64, library string) (target, revision int64) {
	t.Helper()
	ctx := context.Background()
	if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassBackgroundMedia, func(tx *sql.Tx) error {
		var err error
		target, revision, err = captureVisibilityTargetTx(ctx, tx, classID, library)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return target, revision
}

// vfPresent reports which of ids the generation holds, in the order given.
func vfPresent(t *testing.T, db *sql.DB, classID, generation int64, ids []int64) []int64 {
	t.Helper()
	out := []int64{}
	for _, id := range ids {
		var present bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM compact_visibility_rows WHERE class_id=? AND generation=? AND entity_id=?)`,
			classID, generation, id).Scan(&present); err != nil {
			t.Fatal(err)
		}
		if present {
			out = append(out, id)
		}
	}
	return out
}

type vfJournal struct {
	entity, sequence int64
}

// vfJournalRows reads the class journal in frontier order.
func vfJournalRows(t *testing.T, db *sql.DB, classID int64) []vfJournal {
	t.Helper()
	rows, err := db.Query(`SELECT entity_id,first_sequence FROM compact_visibility_dirty WHERE class_id=? ORDER BY first_sequence,entity_id`, classID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := []vfJournal{}
	for rows.Next() {
		var row vfJournal
		if err = rows.Scan(&row.entity, &row.sequence); err != nil {
			t.Fatal(err)
		}
		out = append(out, row)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func vfSameIDs(got, want []int64) bool {
	return fmt.Sprint(got) == fmt.Sprint(want)
}

// P2-64 visibility frontier, through the real service and the real source
// queue: a maintenance attempt captures its source target and library revision
// once, keeps that target across retries, reflects every event up to it in the
// published generation, and leaves later churn owed to the following pass. The
// published stamp is the captured revision, never a revision read after the
// captured target, and a rebuild captures and publishes the same exact window.
func TestVisibilityFrontierPublishesTheCapturedTargetOnly(t *testing.T) {
	c := catalogtest.Open(t)
	films := c.Library("films", "Films", "movie", "/films")
	movies := make([]catalogtest.Item, 8)
	for i := range movies {
		movies[i] = c.Movie(films, fmt.Sprintf("/films/%02d.mkv", i), fmt.Sprintf("Title %02d", i), 1990+i)
	}
	blocked := []catalogtest.Item{
		c.Movie(films, "/films/blocked-0.mkv", "Blocked Zero", 1970),
		c.Movie(films, "/films/blocked-1.mkv", "Blocked One", 1971),
	}
	c.Attributes(blocked[0].ID, "label", "violence")
	c.Attributes(blocked[1].ID, "label", "violence")
	c.Drain()

	s := New(c.DB)
	ctx := context.Background()
	r := identity.ContentRestrictions{BlockedLabels: []string{"violence"}}
	if err := s.RebuildVisibilityClass(ctx, "films", r); err != nil {
		t.Fatal(err)
	}
	classKey, _ := visibilityClassKey("films", r)
	built := vfClassState(t, c.DB, classKey)
	if built.generation == 0 {
		t.Fatal("the first build published no generation")
	}
	if built.captured != built.sourceTarget {
		t.Fatalf("build published captured_sequence %d for target %d", built.captured, built.sourceTarget)
	}
	if got := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=?`, built.id, built.generation); got != int64(len(movies)) {
		t.Fatalf("built class holds %d rows, want %d", got, len(movies))
	}

	// Events before the capture: one title leaves, one blocked title joins and
	// a new title arrives. They are expanded into the class journal, not yet
	// into the published generation.
	c.Attributes(movies[0].ID, "label", "violence") // leaves
	c.Attributes(blocked[0].ID, "label")            // joins
	added := c.Movie(films, "/films/added.mkv", "Added Before Capture", 2001)
	c.Drain()
	if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_source_jobs`); n != 0 {
		t.Fatalf("source queue after the first drain: %d keys", n)
	}

	captured, capturedRevision := vfCaptureTarget(t, c.DB, built.id, "films")
	if captured <= built.captured {
		t.Fatalf("capture %d did not advance past the built target %d", captured, built.captured)
	}
	if captured != vfSourceSequence(t, c.DB) {
		t.Fatalf("captured target %d, accepted clock %d", captured, vfSourceSequence(t, c.DB))
	}
	if capturedRevision != vfLibraryRevision(t, c.DB, "films") {
		t.Fatalf("captured revision %d, current %d", capturedRevision, vfLibraryRevision(t, c.DB, "films"))
	}
	if state := vfClassState(t, c.DB, classKey); state.sourceTarget != captured || state.sourceRevision != capturedRevision {
		t.Fatalf("class target/revision %d/%d, captured %d/%d", state.sourceTarget, state.sourceRevision, captured, capturedRevision)
	}
	// A later revision is the churn the published stamp must not reach for.
	c.Exec(`UPDATE library_revisions SET revision=revision+1 WHERE library_id='films'`)
	laterRevision := vfLibraryRevision(t, c.DB, "films")
	if laterRevision <= capturedRevision {
		t.Fatalf("later revision %d did not advance past the captured %d", laterRevision, capturedRevision)
	}

	// Later churn, after the capture: one title leaves, one blocked title joins
	// and a new title arrives. None of it belongs to the captured window.
	c.Attributes(movies[1].ID, "label", "violence") // leaves, stays owed
	c.Attributes(blocked[1].ID, "label")            // joins, stays owed
	pending := c.Movie(films, "/films/pending.mkv", "Added After Capture", 2002)
	c.Drain()
	if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_source_jobs`); n != 0 {
		t.Fatalf("source queue after the second drain: %d keys", n)
	}
	retryTarget, retryRevision := vfCaptureTarget(t, c.DB, built.id, "films")
	if retryTarget != captured || retryRevision != capturedRevision {
		t.Fatalf("a retry captured %d/%d, the attempt already held %d/%d", retryTarget, retryRevision, captured, capturedRevision)
	}
	if state := vfClassState(t, c.DB, classKey); state.sourceTarget != captured || state.sourceRevision != capturedRevision {
		t.Fatalf("later churn moved the class capture to %d/%d", state.sourceTarget, state.sourceRevision)
	}

	// The incremental pass finishes the old target. It must stay incremental
	// (no generation switch), publish exactly the captured target and stamp the
	// captured revision, although the library has moved on.
	if err := s.refreshVisibilityClass(ctx, "films", r, built.id, built.libraryID, built.generation); err != nil {
		t.Fatal(err)
	}
	published := vfClassState(t, c.DB, classKey)
	if published.generation != built.generation {
		t.Fatalf("a small backlog switched generation %d -> %d", built.generation, published.generation)
	}
	if published.captured != captured {
		t.Fatalf("incremental published captured_sequence %d, want the captured target %d", published.captured, captured)
	}
	if published.sourceTarget != captured {
		t.Fatalf("incremental left source_target %d, want %d", published.sourceTarget, captured)
	}
	if published.revision != capturedRevision {
		t.Fatalf("incremental stamped revision %d, want the captured %d (library is at %d)", published.revision, capturedRevision, laterRevision)
	}
	if published.captured > vfSourceSequence(t, c.DB) {
		t.Fatalf("captured_sequence %d is in the future of the source clock %d", published.captured, vfSourceSequence(t, c.DB))
	}

	all := append([]int64{}, movies[0].ID, movies[1].ID, movies[2].ID, movies[3].ID, movies[4].ID, movies[5].ID, movies[6].ID, movies[7].ID)
	all = append(all, blocked[0].ID, blocked[1].ID, added.ID, pending.ID)
	// The captured window: the departure, the join and the arrival are in it.
	// Later churn (movies[1] leaving, blocked[1] joining, pending arriving)
	// waits for the next pass.
	firstWindow := []int64{movies[1].ID, movies[2].ID, movies[3].ID, movies[4].ID, movies[5].ID, movies[6].ID, movies[7].ID, blocked[0].ID, added.ID}
	if got := vfPresent(t, c.DB, built.id, published.generation, all); !vfSameIDs(got, firstWindow) {
		t.Fatalf("captured window holds %v, want %v", got, firstWindow)
	}
	journal := vfJournalRows(t, c.DB, built.id)
	owed := map[int64]bool{}
	for _, row := range journal {
		if row.sequence <= captured {
			t.Errorf("journal event %d for entity %d survived its captured target %d", row.sequence, row.entity, captured)
		}
		owed[row.entity] = true
	}
	for _, id := range []int64{movies[1].ID, blocked[1].ID, pending.ID} {
		if !owed[id] {
			t.Errorf("later churn for entity %d is not owed to the next pass", id)
		}
	}
	if len(journal) != 3 {
		t.Fatalf("journal holds %d events after the captured pass, want the 3 later ones", len(journal))
	}

	// The next attempt captures what the first one left owed and publishes it.
	target, revision := vfCaptureTarget(t, c.DB, built.id, "films")
	if target <= captured {
		t.Fatalf("the second capture %d did not advance past %d", target, captured)
	}
	if revision != vfLibraryRevision(t, c.DB, "films") {
		t.Fatalf("second captured revision %d, current %d", revision, vfLibraryRevision(t, c.DB, "films"))
	}
	if err := s.refreshVisibilityClass(ctx, "films", r, built.id, built.libraryID, built.generation); err != nil {
		t.Fatal(err)
	}
	second := vfClassState(t, c.DB, classKey)
	if second.generation != built.generation || second.captured != target || second.revision != revision {
		t.Fatalf("second pass generation/target/revision %d/%d/%d, want %d/%d/%d", second.generation, second.captured, second.revision, built.generation, target, revision)
	}
	if n := len(vfJournalRows(t, c.DB, built.id)); n != 0 {
		t.Fatalf("journal still holds %d events after the second pass", n)
	}
	secondWindow := []int64{movies[2].ID, movies[3].ID, movies[4].ID, movies[5].ID, movies[6].ID, movies[7].ID, blocked[0].ID, blocked[1].ID, added.ID, pending.ID}
	if got := vfPresent(t, c.DB, built.id, second.generation, all); !vfSameIDs(got, secondWindow) {
		t.Fatalf("second window holds %v, want %v", got, secondWindow)
	}

	// A rebuild captures and publishes the same exact window: no target past
	// the source clock and no revision the library has not reached.
	if err := s.RebuildVisibilityClass(ctx, "films", r); err != nil {
		t.Fatal(err)
	}
	rebuilt := vfClassState(t, c.DB, classKey)
	if rebuilt.generation <= second.generation {
		t.Fatalf("rebuild did not publish a new generation: %d -> %d", second.generation, rebuilt.generation)
	}
	if rebuilt.captured != vfSourceSequence(t, c.DB) {
		t.Fatalf("rebuilt captured_sequence %d, accepted clock %d", rebuilt.captured, vfSourceSequence(t, c.DB))
	}
	if rebuilt.sourceTarget != rebuilt.captured {
		t.Fatalf("rebuilt target %d differs from captured_sequence %d", rebuilt.sourceTarget, rebuilt.captured)
	}
	if rebuilt.revision != vfLibraryRevision(t, c.DB, "films") {
		t.Fatalf("rebuilt revision %d, current %d", rebuilt.revision, vfLibraryRevision(t, c.DB, "films"))
	}
	if rebuilt.captured > vfSourceSequence(t, c.DB) {
		t.Fatalf("rebuilt captured_sequence %d is in the future of the source clock %d", rebuilt.captured, vfSourceSequence(t, c.DB))
	}
	if got := vfPresent(t, c.DB, rebuilt.id, rebuilt.generation, all); !vfSameIDs(got, secondWindow) {
		t.Fatalf("rebuilt window holds %v, want %v", got, secondWindow)
	}
	if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_rows WHERE class_id=? AND generation=?`, rebuilt.id, second.generation); n != 0 {
		t.Fatalf("the replaced generation kept %d rows", n)
	}
	if n := len(vfJournalRows(t, c.DB, rebuilt.id)); n != 0 {
		t.Fatalf("journal holds %d events after the rebuild", n)
	}
	checkAnchors(t, c.DB, rebuilt.id, rebuilt.generation)
}

// Every publication below has newer source work still queued. An appended
// title cannot extend the already captured window or require a global quiet
// gap before the prior window becomes readable.
func TestVisibilityFrontierAdvancesWithAppendedSourceWork(t *testing.T) {
	c := catalogtest.Open(t)
	lib := c.Library("appends", "Appends", "movie", "/appends")
	item := c.Movie(lib, "/appends/first.mkv", "First", 2000)
	c.Drain()
	s := New(c.DB)
	ctx := context.Background()
	r := identity.ContentRestrictions{BlockedLabels: []string{"blocked"}}
	if err := s.RebuildVisibilityClass(ctx, "appends", r); err != nil {
		t.Fatal(err)
	}
	key, _ := visibilityClassKey("appends", r)
	previous := vfClassState(t, c.DB, key).captured
	for round := 0; round < 3; round++ {
		c.Fields(item.ID, map[string]any{"title": fmt.Sprintf("Captured %d", round)})
		c.Drain()
		before := vfClassState(t, c.DB, key)
		target, revision := vfCaptureTarget(t, c.DB, before.id, "appends")
		if target <= previous {
			t.Fatalf("target did not advance: %d -> %d", previous, target)
		}
		// This is real appended source input after capture, not a synthetic queue.
		c.Movie(lib, fmt.Sprintf("/appends/later-%d.mkv", round), fmt.Sprintf("Later %d", round), 2001+round)
		if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_source_jobs WHERE first_pending>?`, target); n == 0 {
			t.Fatal("fixture has no later pending source event")
		}
		if err := s.refreshVisibilityClass(ctx, "appends", r, before.id, before.libraryID, before.generation); err != nil {
			t.Fatal(err)
		}
		after := vfClassState(t, c.DB, key)
		if after.captured != target || after.revision != revision {
			t.Fatalf("published target/revision %d/%d, captured %d/%d", after.captured, after.revision, target, revision)
		}
		if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_source_jobs WHERE first_pending>?`, target); n == 0 {
			t.Fatal("publication consumed later work instead of leaving it for the next window")
		}
		if n := vfCount(t, c.DB, `SELECT count(*) FROM compact_visibility_rows v JOIN catalog_browse_rows b ON b.entity_id=v.entity_id WHERE v.class_id=? AND v.generation=? AND v.entity_id=? AND v.sort_key=b.sort_key`, after.id, after.generation, item.ID); n != 1 {
			t.Fatal("captured source edit was skipped")
		}
		previous = target
	}
}
