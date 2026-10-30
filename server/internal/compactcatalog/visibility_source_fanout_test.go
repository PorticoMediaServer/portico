package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
)

// Visibility source fanout fixtures. Jobs are seeded through the real schema so
// the production triggers and indexes are the oracle; only the worker call is
// ours. cc1* helpers come from listening_context_test.go.

type visibilityFanoutJob struct {
	found                                                        bool
	sequence, firstPending, passSequence, rerunFirst             int64
	cursorEntity, cursorSource, fenceEntity, fenceSource, termID int64
	phase                                                        int
}

func visibilityFanoutStep(t *testing.T, db *sql.DB, limit int) int {
	t.Helper()
	n := 0
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		n, err = StepVisibilitySourceFanout(ctx, tx, limit)
		return err
	})
	return n
}

func visibilityFanoutReady(t *testing.T, db *sql.DB, target int64) bool {
	t.Helper()
	var ready bool
	cc1Write(t, db, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		ready, err = VisibilitySourceTargetReadyTx(ctx, tx, target)
		return err
	})
	return ready
}

func visibilityFanoutClock(t *testing.T, db *sql.DB) int64 {
	t.Helper()
	sequence, err := VisibilitySourceClock(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return sequence
}

// visibilityFanoutReset clears queued work and pins the clock so a test controls
// the exact event ids a job and its children will inherit.
func visibilityFanoutReset(t *testing.T, db *sql.DB, sequence int64) {
	t.Helper()
	cc1Exec(t, db, `DELETE FROM compact_visibility_source_jobs`)
	cc1Exec(t, db, `DELETE FROM compact_visibility_dirty`)
	cc1Exec(t, db, `UPDATE compact_visibility_source_state SET sequence=?,turn=0 WHERE id=1`, sequence)
}

func visibilityFanoutInsertJob(t *testing.T, db *sql.DB, kind int, library, subject int64, text string, sequence, firstPending int64) {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO compact_visibility_source_jobs(kind,library_id,subject_id,subject_text,sequence,first_pending)
 VALUES(?,?,?,?,?,?)`, kind, library, subject, text, sequence, firstPending)
}

func visibilityFanoutJobOf(t *testing.T, db *sql.DB, kind int, library, subject int64, text string) visibilityFanoutJob {
	t.Helper()
	var j visibilityFanoutJob
	err := db.QueryRow(`SELECT sequence,first_pending,pass_sequence,rerun_first,cursor_phase,cursor_entity,cursor_source,
 fence_entity,fence_source,term_id FROM compact_visibility_source_jobs
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=?`, kind, library, subject, text).
		Scan(&j.sequence, &j.firstPending, &j.passSequence, &j.rerunFirst, &j.phase,
			&j.cursorEntity, &j.cursorSource, &j.fenceEntity, &j.fenceSource, &j.termID)
	if err == sql.ErrNoRows {
		return j
	}
	if err != nil {
		t.Fatal(err)
	}
	j.found = true
	return j
}

func visibilityFanoutBrowseRow(t *testing.T, db *sql.DB, entity, library, kind int64) {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO catalog_browse_rows(entity_id,library_id,kind,title,sort_key,head,year,
 rating_key,label_key,available,recent,backdrop) VALUES(?,?,?,'Row','row','r',2000,'','',1,1,1)`, entity, library, kind)
}

func visibilityFanoutMember(t *testing.T, db *sql.DB, entity, item, source int64) {
	t.Helper()
	cc1Exec(t, db, `INSERT INTO catalog_browse_memberships(entity_id,item_id,source) VALUES(?,?,?)`, entity, item, source)
}

// visibilityFanoutPin makes the next call try kind first. Round robin is
// deliberately fair and may serve a freshly queued child before an interrupted
// parent; a parent-cursor test pins the kind instead of weakening that rule.
func visibilityFanoutPin(t *testing.T, db *sql.DB, kind int) {
	t.Helper()
	cc1Exec(t, db, `UPDATE compact_visibility_source_state SET turn=? WHERE id=1`, kind-1)
}

// The raw class page spends the whole quantum even though a retired class owes
// nothing, and the complete id cursor moves past it. min(limit,8) is a hard
// page bound regardless of the caller's larger limit.
func TestVisibilitySourceFanoutRowRawPageBoundAndRetiredReject(t *testing.T) {
	db := cc1OpenDB(t, "visibility-row-bound.sqlite")
	lib := cc1Library(t, db, "lib", "Films", "movie", "/films")
	classes := make([]int64, 0, 10)
	for i := 0; i < 10; i++ {
		classes = append(classes, cc1Class(t, db, lib, fmt.Sprintf("class-%02d", i)))
	}
	entity := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:row", Title: "Row", Year: 2000}, nil)
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 1, lib, entity, "", 1, 1)
	cc1Exec(t, db, `UPDATE compact_visibility_classes SET retired=1 WHERE id=?`, classes[8])

	if n := visibilityFanoutStep(t, db, 500); n != 8 {
		t.Fatalf("first call consumed %d raw classes, want the 8-row quantum", n)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_dirty`); got != 8 {
		t.Fatalf("first page dirtied %d classes, want 8", got)
	}
	job := visibilityFanoutJobOf(t, db, 1, lib, entity, "")
	if !job.found || job.passSequence != 1 || job.cursorEntity != classes[7] || job.cursorSource != -1 {
		t.Fatalf("active pass=%+v, want pass_sequence 1 and cursor at class %d", job, classes[7])
	}
	if visibilityFanoutReady(t, db, 1) {
		t.Fatal("readiness reported a captured target while the pass was active")
	}

	if n := visibilityFanoutStep(t, db, 500); n != 2 {
		t.Fatalf("second call consumed %d raw classes, want 2", n)
	}
	// Ten raw class ids were charged and only the nine live ones are dirty.
	if job = visibilityFanoutJobOf(t, db, 1, lib, entity, ""); job.found {
		t.Fatalf("finished key still queued: %+v", job)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_dirty`); got != 9 {
		t.Fatalf("dirty classes=%d, want 9 (the retired class is a charged reject)", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_dirty WHERE first_sequence=1`); got != 9 {
		t.Fatalf("%d dirty markers kept the parent frontier, want 9", got)
	}
	if !visibilityFanoutReady(t, db, 1) {
		t.Fatal("captured target not ready after the key finished")
	}
}

// The membership page is the complete (entity_id,source) tuple: a repeated
// container coalesces to one child, a missing container is a charged reject,
// and the cursor still advances over every raw tuple.
func TestVisibilitySourceFanoutMemberTuplesCoalesceAndReject(t *testing.T) {
	db := cc1OpenDB(t, "visibility-member.sqlite")
	lib := cc1Library(t, db, "lib", "Shows", "tv", "/shows")
	missing := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:missing", Title: "Missing", Year: 2000}, nil)
	container := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:container", Title: "Container", Year: 2000}, nil)
	other := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:other", Title: "Other", Year: 2000}, nil)
	item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:item", Title: "Item", Year: 2000}, nil)
	visibilityFanoutBrowseRow(t, db, container, lib, 2)
	visibilityFanoutBrowseRow(t, db, other, lib, 3)
	visibilityFanoutBrowseRow(t, db, item, lib, 1)
	visibilityFanoutMember(t, db, missing, item, 0)
	visibilityFanoutMember(t, db, container, item, 0)
	visibilityFanoutMember(t, db, container, item, 1)
	visibilityFanoutMember(t, db, other, item, 0)
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 2, 0, item, "", 1, 1)

	// The item's own-row child is queued by phase 0 and would win the next
	// round-robin turn, so pin the kind being observed. Fairness itself is
	// covered by the worker-level test.
	visibilityFanoutPin(t, db, 2)
	if n := visibilityFanoutStep(t, db, 2); n != 2 {
		t.Fatalf("first tuple page consumed %d raw memberships, want 2", n)
	}
	job := visibilityFanoutJobOf(t, db, 2, 0, item, "")
	if !job.found || job.phase != 1 || job.cursorEntity != container || job.cursorSource != 0 {
		t.Fatalf("active pass=%+v, want phase 1 cursor at (%d,0)", job, container)
	}
	// The item's own row plus the accepted container. The missing entity is the
	// first raw tuple and still consumed a slot.
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=1`); got != 2 {
		t.Fatalf("children after first page=%d, want own row and the first container", got)
	}

	visibilityFanoutPin(t, db, 2)
	if n := visibilityFanoutStep(t, db, 2); n != 2 {
		t.Fatalf("second tuple page consumed %d raw memberships, want 2", n)
	}
	if job = visibilityFanoutJobOf(t, db, 2, 0, item, ""); job.found {
		t.Fatalf("finished item key still queued: %+v", job)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=1`); got != 3 {
		t.Fatalf("kind 1 children=%d, want the own row plus two distinct containers", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=1 AND sequence=1 AND first_pending=1`); got != 3 {
		t.Fatalf("%d children inherited the parent frontier, want 3", got)
	}
}

// Kind 3 applies the container scope AFTER the raw tuple: a movie row and a
// missing row are charged rejections, and only the show/season container is
// enqueued.
func TestVisibilitySourceFanoutContainerScopeRejects(t *testing.T) {
	db := cc1OpenDB(t, "visibility-container.sqlite")
	lib := cc1Library(t, db, "lib", "Shows", "tv", "/shows")
	missing := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:missing", Title: "Missing", Year: 2000}, nil)
	show := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:show", Title: "Show", Year: 2000}, nil)
	movie := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:movie", Title: "Movie", Year: 2000}, nil)
	item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:item", Title: "Item", Year: 2000}, nil)
	visibilityFanoutBrowseRow(t, db, show, lib, 2)
	visibilityFanoutBrowseRow(t, db, movie, lib, 1)
	visibilityFanoutMember(t, db, missing, item, 0)
	visibilityFanoutMember(t, db, show, item, 0)
	visibilityFanoutMember(t, db, movie, item, 0)
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 3, 0, item, "", 1, 1)

	if n := visibilityFanoutStep(t, db, 500); n != 3 {
		t.Fatalf("consumed %d raw memberships, want all 3 (rejects included)", n)
	}
	if job := visibilityFanoutJobOf(t, db, 3, 0, item, ""); job.found {
		t.Fatalf("finished container key still queued: %+v", job)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=1`); got != 1 {
		t.Fatalf("kind 3 enqueued %d containers, want only the show row", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=1 AND subject_id=? AND library_id=?`, show, lib); got != 1 {
		t.Fatalf("child for the show container=%d, want 1", got)
	}
}

// A source write during an active pass must not reset the cursor or the captured
// sequence. It only records the earliest rerun event; the end of the pass keeps
// that event owed, and the captured target is ready even while the rerun is
// still queued.
func TestVisibilitySourceFanoutRerunDuringActivePass(t *testing.T) {
	db := cc1OpenDB(t, "visibility-rerun.sqlite")
	lib := cc1Library(t, db, "lib", "Films", "movie", "/films")
	classes := make([]int64, 0, 9)
	for i := 0; i < 9; i++ {
		classes = append(classes, cc1Class(t, db, lib, fmt.Sprintf("class-%02d", i)))
	}
	entity := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:rerun", Title: "Rerun", Year: 2000}, nil)
	visibilityFanoutBrowseRow(t, db, entity, lib, 1)
	visibilityFanoutReset(t, db, 4)
	visibilityFanoutInsertJob(t, db, 1, lib, entity, "", 4, 4)

	if n := visibilityFanoutStep(t, db, 8); n != 8 {
		t.Fatalf("first call consumed %d classes, want 8", n)
	}
	job := visibilityFanoutJobOf(t, db, 1, lib, entity, "")
	if !job.found || job.passSequence != 4 || job.cursorEntity != classes[7] {
		t.Fatalf("active pass=%+v, want captured sequence 4 and cursor %d", job, classes[7])
	}

	// A real accepted source write behind the cursor: the trigger bumps the
	// clock and records the rerun, but must leave pass_sequence and the cursor.
	cc1Exec(t, db, `UPDATE catalog_browse_rows SET sort_key='changed' WHERE entity_id=?`, entity)
	if got := visibilityFanoutClock(t, db); got != 5 {
		t.Fatalf("clock after churn=%d, want 5", got)
	}
	job = visibilityFanoutJobOf(t, db, 1, lib, entity, "")
	if !job.found || job.passSequence != 4 || job.cursorEntity != classes[7] {
		t.Fatalf("churn restarted the pass: %+v", job)
	}
	if job.sequence != 5 || job.firstPending != 4 || job.rerunFirst != 5 {
		t.Fatalf("churn job sequence=%d first_pending=%d rerun_first=%d, want 5/4/5", job.sequence, job.firstPending, job.rerunFirst)
	}
	if visibilityFanoutReady(t, db, 4) || visibilityFanoutReady(t, db, 5) {
		t.Fatal("churn event was reported ready before the pass finished")
	}

	// The remainder of the captured image ends the pass. The rerun event stays
	// owed and the fixed target 4 stops blocking.
	if n := visibilityFanoutStep(t, db, 8); n != 1 {
		t.Fatalf("resumed call consumed %d classes, want 1", n)
	}
	job = visibilityFanoutJobOf(t, db, 1, lib, entity, "")
	if !job.found || job.firstPending != 5 || job.passSequence != 0 || job.rerunFirst != 0 ||
		job.sequence != 5 || job.cursorEntity != 0 || job.cursorSource != -1 ||
		job.fenceEntity != 0 || job.fenceSource != -1 || job.termID != 0 {
		t.Fatalf("rerun-first job=%+v, want first_pending 5 with reset cursors/fences", job)
	}
	if !visibilityFanoutReady(t, db, 4) {
		t.Fatal("captured target 4 still blocked by a later event")
	}
	if visibilityFanoutReady(t, db, 5) {
		t.Fatal("rerun event 5 reported ready before its rerun pass")
	}

	// The rerun restarts from the beginning and covers the whole image, so the
	// final key is deleted. Every dirty marker keeps the earliest event.
	if n := visibilityFanoutStep(t, db, 8); n != 8 {
		t.Fatalf("rerun first page consumed %d classes, want 8", n)
	}
	if n := visibilityFanoutStep(t, db, 8); n != 1 {
		t.Fatalf("rerun last page consumed %d classes, want 1", n)
	}
	if job = visibilityFanoutJobOf(t, db, 1, lib, entity, ""); job.found {
		t.Fatalf("rerun key still queued: %+v", job)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_dirty WHERE first_sequence=4`); got != 9 {
		t.Fatalf("%d dirty markers kept the earliest frontier, want 9", got)
	}
	if !visibilityFanoutReady(t, db, 5) {
		t.Fatal("rerun event stayed unready after the rerun finished")
	}
}

// A kind 2 child inherits the parent's pending marker and captured sequence and
// never advances the global clock. The child must not wait behind later events
// that happened to already be on the clock when the parent was expanded.
func TestVisibilitySourceFanoutChildInheritsParentFrontier(t *testing.T) {
	db := cc1OpenDB(t, "visibility-child-frontier.sqlite")
	lib := cc1Library(t, db, "lib", "Shows", "tv", "/shows")
	container := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:container", Title: "Container", Year: 2000}, nil)
	item := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:item", Title: "Item", Year: 2000}, nil)
	visibilityFanoutBrowseRow(t, db, container, lib, 2)
	visibilityFanoutMember(t, db, container, item, 0)
	visibilityFanoutReset(t, db, 3)
	// The clock is far ahead (later unrelated events), the parent event is not.
	cc1Exec(t, db, `UPDATE compact_visibility_source_state SET sequence=9 WHERE id=1`)
	visibilityFanoutInsertJob(t, db, 2, 0, item, "", 3, 3)

	if n := visibilityFanoutStep(t, db, 8); n != 1 {
		t.Fatalf("consumed %d raw memberships, want 1", n)
	}
	if got := visibilityFanoutClock(t, db); got != 9 {
		t.Fatalf("child fanout advanced the clock to %d, want 9", got)
	}
	child := visibilityFanoutJobOf(t, db, 1, lib, container, "")
	if !child.found || child.sequence != 3 || child.firstPending != 3 || child.passSequence != 0 {
		t.Fatalf("child=%+v, want inherited sequence/first_pending 3", child)
	}
	if visibilityFanoutReady(t, db, 3) {
		t.Fatal("inherited child frontier reported ready before the child ran")
	}
}

// Kind 4 resolves the unique contentRating term once, walks its raw item edges
// and queues kind 2 for each item; a value with no term is acknowledged, not
// parked forever.
func TestVisibilitySourceFanoutRatingAgeTerm(t *testing.T) {
	db := cc1OpenDB(t, "visibility-rating-age.sqlite")
	lib := cc1Library(t, db, "lib", "Films", "movie", "/films")
	first := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:first", Title: "First", Year: 2000}, nil)
	second := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:second", Title: "Second", Year: 2000}, nil)
	cc1Exec(t, db, `INSERT INTO catalog_attribute_terms(field_id,value_key) VALUES(1,'pg')`)
	var term int64
	if err := db.QueryRow(`SELECT id FROM catalog_attribute_terms WHERE field_id=1 AND value_key='pg'`).Scan(&term); err != nil {
		t.Fatal(err)
	}
	cc1Exec(t, db, `INSERT INTO catalog_item_attribute_edges(item_id,term_id,source_value,source_rowid) VALUES(?,?,'pg',1),(?,?,'pg',2)`, first, term, second, term)
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 4, 0, 0, "pg", 1, 1)

	if n := visibilityFanoutStep(t, db, 500); n != 2 {
		t.Fatalf("consumed %d raw item edges, want 2", n)
	}
	if job := visibilityFanoutJobOf(t, db, 4, 0, 0, "pg"); job.found {
		t.Fatalf("finished rating value still queued: %+v", job)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs WHERE kind=2 AND first_pending=1 AND sequence=1`); got != 2 {
		t.Fatalf("kind 4 queued %d kind 2 children, want 2", got)
	}
	if got := visibilityFanoutClock(t, db); got != 1 {
		t.Fatalf("kind 4 advanced the clock to %d, want 1", got)
	}

	// A gone term cannot have an edge yet; the key is acknowledged and a later
	// edge insert queues its own kind 2 key from the source trigger.
	visibilityFanoutReset(t, db, 1)
	visibilityFanoutInsertJob(t, db, 4, 0, 0, "gone", 1, 1)
	if n := visibilityFanoutStep(t, db, 500); n != 1 {
		t.Fatalf("gone term step returned %d, want an acknowledgement", n)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); got != 0 {
		t.Fatalf("gone term left %d jobs queued", got)
	}
}

// The browse row trigger is semantic: a column that is not copied into derived
// metrics queues nothing, while a copied metric column queues the row key.
func TestVisibilitySourceFanoutSemanticTriggerScope(t *testing.T) {
	db := cc1OpenDB(t, "visibility-trigger-scope.sqlite")
	lib := cc1Library(t, db, "lib", "Films", "movie", "/films")
	entity := cc1Entity(t, db, Entity{Library: lib, Kind: Movie, Key: "film:trigger", Title: "Trigger", Year: 2000}, nil)
	visibilityFanoutBrowseRow(t, db, entity, lib, 1)
	visibilityFanoutReset(t, db, 3)

	cc1Exec(t, db, `UPDATE catalog_browse_rows SET title='Renamed',backdrop=1,release_key='2026',file_size=999,resolution_key=2160 WHERE entity_id=?`, entity)
	if got := visibilityFanoutClock(t, db); got != 3 {
		t.Fatalf("non-copied title changed the clock to %d, want 3", got)
	}
	if got := cc1Scalar(t, db, `SELECT count(*) FROM compact_visibility_source_jobs`); got != 0 {
		t.Fatalf("non-copied title queued %d jobs, want 0", got)
	}

	cc1Exec(t, db, `UPDATE catalog_browse_rows SET duration_max=12.5 WHERE entity_id=?`, entity)
	if got := visibilityFanoutClock(t, db); got != 4 {
		t.Fatalf("copied duration_max left clock at %d, want 4", got)
	}
	if job := visibilityFanoutJobOf(t, db, 1, lib, entity, ""); !job.found || job.sequence != 4 || job.firstPending != 4 {
		t.Fatalf("copied metric job=%+v, want sequence/first_pending 4", job)
	}

	cc1Exec(t, db, `UPDATE catalog_browse_rows SET rating_max=7.5 WHERE entity_id=?`, entity)
	if job := visibilityFanoutJobOf(t, db, 1, lib, entity, ""); !job.found || job.sequence != 5 || job.firstPending != 4 {
		t.Fatalf("coalesced job=%+v, want latest sequence 5 and earliest pending 4", job)
	}
}
