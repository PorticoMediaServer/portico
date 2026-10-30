package compactcatalog

// Bounded visibility source fanout (P2-64, C-r2).
//
// Accepted source writers only coalesce one job key each: catalog_browse_rows
// (kind 1), catalog_browse_memberships + attribute edges (kind 2), availability
// and asset links (kind 3), content_rating_ages (kind 4). Enumerating a library's
// classes or an item's containers in the writer transaction is exactly the
// O(K)/O(C*K) behaviour this queue removes. This step is the only writer that
// expands a key, one job and one bounded raw page per call.
//
// A pass is a finite walk over the raw keys that existed when it started:
//
//	pass_sequence  the key's event sequence captured once, at pass start
//	fence          the raw high-water key captured at the same moment
//	cursor         last raw key consumed (advanced over rejects as well)
//
// The fence is never moved by later appends, so a pass cannot chase an
// ever-growing source. A source write during an active pass leaves the captured
// sequence, the fence and the cursor untouched; the trigger only records the
// earliest such event in rerun_first (see the baseline triggers). When the pass
// ends, a recorded rerun restarts the walk from the beginning, and its own
// bounded pass recaptures both the sequence and the fence. That is the exact
// oldest-first/no-skipped-event rule: the completed pass only claims the
// frontier it captured, and everything appended or rewritten later stays owed.
//
// Child enqueues inherit the parent's first_pending and captured pass_sequence.
// They never advance the global clock: the parent's event is being expanded in
// the frontier window it belongs to, so the child cannot wait behind events
// that arrived after the parent. The upsert keeps the latest sequence, the
// earliest pending event, and (while a child pass is active) the earliest
// rerun event.
//
// Round robin over the four kinds plus first_pending ordering (the
// compact_visibility_source_jobs_turn index) means one call never serves the
// same hot key twice in a row while another kind has work.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Visibility source kinds match the check constraint on
// compact_visibility_source_jobs.kind.
const (
	visibilitySourceKindRow        = 1 // browse row/entity -> classes of its library
	visibilitySourceKindItem       = 2 // item -> own row, then every container membership
	visibilitySourceKindContainer  = 3 // item -> show/season container memberships
	visibilitySourceKindRatingAge  = 4 // contentRating value -> items with that term
	visibilitySourceKinds          = 4
	visibilitySourceMemberNoCursor = -1
)

// visibilitySourceJob is one coalesced key. sequence is the latest event,
// first_pending the earliest event not covered by a finished pass,
// pass_sequence the frontier of the pass in flight (0 = none) and rerun_first
// the earliest event observed while that pass was in flight.
type visibilitySourceJob struct {
	kind         int
	library      int64
	subject      int64
	text         string
	sequence     int64
	firstPending int64
	passSequence int64
	rerunFirst   int64
	phase        int
	cursorEntity int64
	cursorSource int64
	fenceEntity  int64
	fenceSource  int64
	termID       int64
}

// The turn index is (kind,first_pending,library_id,subject_id,subject_text), so
// this is both an index seek and the exact oldest-first order for the kind.
const takeVisibilitySourceJobSQL = `SELECT kind,library_id,subject_id,subject_text,sequence,first_pending,
 pass_sequence,rerun_first,cursor_phase,cursor_entity,cursor_source,fence_entity,fence_source,term_id
 FROM compact_visibility_source_jobs INDEXED BY compact_visibility_source_jobs_turn
 WHERE kind=? ORDER BY first_pending,library_id,subject_id,subject_text LIMIT 1`

// beginVisibilitySourcePassSQL captures the sequence the pass will cover. The
// guard makes a stale read (or an already active pass) a no-op rather than a
// second concurrent pass over the same key.
const beginVisibilitySourcePassSQL = `UPDATE compact_visibility_source_jobs
 SET pass_sequence=?,fence_entity=?,fence_source=?,term_id=?,cursor_phase=0,cursor_entity=0,cursor_source=-1
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=? AND sequence=? AND pass_sequence=0`

// checkpointVisibilitySourcePassSQL is guarded by the captured frontier. A
// source write during the pass only sets rerun_first; it never resets these
// cursors, so a resumed pass continues where it stopped.
const checkpointVisibilitySourcePassSQL = `UPDATE compact_visibility_source_jobs
 SET cursor_phase=?,cursor_entity=?,cursor_source=?
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=? AND pass_sequence=?`

// deleteFinishedVisibilitySourcePassSQL removes a key whose captured frontier
// was fully covered and that saw no write during the pass. The sequence guard
// keeps a row that a trigger touched after the read.
const deleteFinishedVisibilitySourcePassSQL = `DELETE FROM compact_visibility_source_jobs
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=?
 AND pass_sequence=? AND rerun_first=0 AND sequence=?`

// rerunVisibilitySourcePassSQL restarts a key at the earliest event observed
// during the pass. The latest sequence is retained; cursors and fences reset so
// the rerun walks the new raw image from the beginning.
const rerunVisibilitySourcePassSQL = `UPDATE compact_visibility_source_jobs
 SET first_pending=rerun_first,pass_sequence=0,rerun_first=0,
  cursor_phase=0,cursor_entity=0,cursor_source=-1,fence_entity=0,fence_source=-1,term_id=0
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=? AND pass_sequence=? AND rerun_first>0`

// A trigger that bumps sequence without recording a rerun is not possible in
// the accepted schema. Keeping the key anyway is the conservative choice: the
// next pass covers from pass_sequence+1 and cannot skip an event.
const rereadVisibilitySourcePassSQL = `UPDATE compact_visibility_source_jobs
 SET first_pending=?,pass_sequence=0,rerun_first=0,
  cursor_phase=0,cursor_entity=0,cursor_source=-1,fence_entity=0,fence_source=-1,term_id=0
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=? AND pass_sequence=? AND sequence>?`

// Child enqueues inherit the parent's pending marker and captured frontier.
// sequence=max because the child may already carry a later event;
// first_pending=min because the earliest owed event must stay owed. While the
// child has a pass in flight, rerun_first keeps the earliest of its existing
// marker and the parent's inherited pending event (never a fresh clock read).
const visibilityChildRowJobSQL = `INSERT INTO compact_visibility_source_jobs(kind,library_id,subject_id,sequence,first_pending)
 SELECT 1,r.library_id,r.entity_id,?2,?3 FROM catalog_browse_rows r WHERE r.entity_id=?1
 ON CONFLICT(kind,library_id,subject_id,subject_text) DO UPDATE SET
  sequence=max(sequence,excluded.sequence),
  first_pending=min(first_pending,excluded.first_pending),
  rerun_first=CASE WHEN pass_sequence=0 THEN rerun_first
   WHEN rerun_first=0 THEN excluded.first_pending
   ELSE min(rerun_first,excluded.first_pending) END`

const visibilityChildContainerJobSQL = `INSERT INTO compact_visibility_source_jobs(kind,library_id,subject_id,sequence,first_pending)
 SELECT 1,r.library_id,r.entity_id,?2,?3 FROM catalog_browse_rows r WHERE r.entity_id=?1 AND r.kind IN(2,3)
 ON CONFLICT(kind,library_id,subject_id,subject_text) DO UPDATE SET
  sequence=max(sequence,excluded.sequence),
  first_pending=min(first_pending,excluded.first_pending),
  rerun_first=CASE WHEN pass_sequence=0 THEN rerun_first
   WHEN rerun_first=0 THEN excluded.first_pending
   ELSE min(rerun_first,excluded.first_pending) END`

const visibilityChildItemJobSQL = `INSERT INTO compact_visibility_source_jobs(kind,subject_id,sequence,first_pending)
 VALUES(2,?1,?2,?3)
 ON CONFLICT(kind,library_id,subject_id,subject_text) DO UPDATE SET
  sequence=max(sequence,excluded.sequence),
  first_pending=min(first_pending,excluded.first_pending),
  rerun_first=CASE WHEN pass_sequence=0 THEN rerun_first
   WHEN rerun_first=0 THEN excluded.first_pending
   ELSE min(rerun_first,excluded.first_pending) END`

// kind 1 raw page: class ids of the key's library, bounded by the captured
// high-water class id. compact_visibility_classes_library is (library_id,id).
const visibilityClassPageSQL = `SELECT id,retired FROM compact_visibility_classes INDEXED BY compact_visibility_classes_library
 WHERE library_id=?1 AND id>?2 AND id<=?3 ORDER BY id LIMIT ?4`

// The dirty marker keeps the parent's earliest pending event. An existing class
// row can be owed by an older event, so the conflict keeps the minimum.
const upsertVisibilityDirtySQL = `INSERT INTO compact_visibility_dirty(class_id,entity_id,first_sequence) VALUES(?1,?2,?3)
 ON CONFLICT(class_id,entity_id) DO UPDATE SET first_sequence=min(first_sequence,excluded.first_sequence)`

// kinds 2/3 raw page: the complete (entity_id,source) tuple, in the index order
// of catalog_browse_membership_item (item_id,entity_id,+PRIMARY KEY tail).
// Nothing is joined, filtered or DISTINCTed before LIMIT, and the captured
// high-water tuple closes the page.
const visibilityMemberPageSQL = `SELECT entity_id,source FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item
 WHERE item_id=?1 AND (entity_id,source)>(?2,?3) AND (entity_id,source)<=(?4,?5)
 ORDER BY entity_id,source LIMIT ?6`

// kind 4 phase 0: one unique (field,value_key) seek, not a scan over terms.
const visibilityRatingAgeTermSQL = `SELECT t.id FROM catalog_attribute_terms t JOIN catalog_attribute_fields f ON f.id=t.field_id
 WHERE f.field='contentRating' AND t.value_key=?1`

// kind 4 phase 1: raw item edges of that term in index order, closed by the
// captured high-water item id.
const visibilityRatingAgeItemPageSQL = `SELECT item_id FROM catalog_item_attribute_edges INDEXED BY catalog_item_attribute_edges_term
 WHERE term_id=?1 AND item_id>?2 AND item_id<=?3 ORDER BY item_id LIMIT ?4`

// StepVisibilitySourceFanout expands one coalesced source key. One call takes
// one job (round robin over the four kinds, oldest first inside a kind), starts
// or resumes its pass, and consumes at most min(limit,8) raw source rows.
//
// Rejections (a retired class, a container that has no browse row or the wrong
// kind, an item whose term is gone) are charged against that raw budget after
// the page is captured, and the complete cursor moves past them. The return
// value is the raw rows consumed, or 1 when a job was acknowledged without raw
// rows, so the worker can treat it as progress.
func StepVisibilitySourceFanout(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
	if limit < 1 {
		return 0, nil
	}
	budget := min(limit, upstreamQuantum)
	var turn int
	if err := tx.QueryRowContext(ctx, `SELECT turn FROM compact_visibility_source_state WHERE id=1`).Scan(&turn); err != nil {
		return 0, err
	}
	turn = ((turn % visibilitySourceKinds) + visibilitySourceKinds) % visibilitySourceKinds
	for i := 0; i < visibilitySourceKinds; i++ {
		kind := (turn+i)%visibilitySourceKinds + 1
		job, found, err := takeVisibilitySourceJob(ctx, tx, kind)
		if err != nil {
			return 0, err
		}
		if !found {
			continue
		}
		n, err := stepVisibilitySourceJob(ctx, tx, &job, budget)
		if err != nil {
			return 0, err
		}
		// Advance past the kind just served even when it only acknowledged an
		// empty or gone key, so a hot key cannot monopolize the rotation.
		if _, err = tx.ExecContext(ctx, `UPDATE compact_visibility_source_state SET turn=? WHERE id=1`, kind%visibilitySourceKinds); err != nil {
			return 0, err
		}
		return max(n, 1), nil
	}
	return 0, nil
}

func takeVisibilitySourceJob(ctx context.Context, tx *sql.Tx, kind int) (visibilitySourceJob, bool, error) {
	job := visibilitySourceJob{kind: kind, cursorSource: visibilitySourceMemberNoCursor, fenceSource: visibilitySourceMemberNoCursor}
	err := tx.QueryRowContext(ctx, takeVisibilitySourceJobSQL, kind).Scan(&job.kind, &job.library, &job.subject, &job.text,
		&job.sequence, &job.firstPending, &job.passSequence, &job.rerunFirst, &job.phase,
		&job.cursorEntity, &job.cursorSource, &job.fenceEntity, &job.fenceSource, &job.termID)
	if errors.Is(err, sql.ErrNoRows) {
		return job, false, nil
	}
	if err != nil {
		return job, false, err
	}
	return job, true, nil
}

// beginVisibilitySourcePass captures the sequence and raw high-water key once
// and returns false when the pass could not be started (a lost race or a gone
// key that was acknowledged). A job that already has pass_sequence is resumed,
// never restarted.
func beginVisibilitySourcePass(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob) (bool, error) {
	job.termID = 0
	job.fenceEntity, job.fenceSource = 0, visibilitySourceMemberNoCursor
	switch job.kind {
	case visibilitySourceKindRow:
		var fence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(id),0) FROM compact_visibility_classes WHERE library_id=?`, job.library).Scan(&fence); err != nil {
			return false, err
		}
		job.fenceEntity = fence
	case visibilitySourceKindItem, visibilitySourceKindContainer:
		var entity, source sql.NullInt64
		err := tx.QueryRowContext(ctx, `SELECT entity_id,source FROM catalog_browse_memberships INDEXED BY catalog_browse_membership_item
 WHERE item_id=? ORDER BY entity_id DESC,source DESC LIMIT 1`, job.subject).Scan(&entity, &source)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return false, err
		}
		if err == nil {
			job.fenceEntity, job.fenceSource = entity.Int64, source.Int64
		}
	case visibilitySourceKindRatingAge:
		var term int64
		err := tx.QueryRowContext(ctx, visibilityRatingAgeTermSQL, job.text).Scan(&term)
		if errors.Is(err, sql.ErrNoRows) {
			// No term means no edge can reference this value yet. A later edge
			// insert queues its own kind 2 key, so acknowledge instead of parking.
			_, err = tx.ExecContext(ctx, `DELETE FROM compact_visibility_source_jobs
 WHERE kind=? AND library_id=? AND subject_id=? AND subject_text=? AND sequence=? AND pass_sequence=0`,
				job.kind, job.library, job.subject, job.text, job.sequence)
			return false, err
		}
		if err != nil {
			return false, err
		}
		job.termID = term
		var fence int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(item_id),0) FROM catalog_item_attribute_edges WHERE term_id=?`, term).Scan(&fence); err != nil {
			return false, err
		}
		job.fenceEntity = fence
	default:
		return false, fmt.Errorf("visibility source job kind %d", job.kind)
	}
	res, err := tx.ExecContext(ctx, beginVisibilitySourcePassSQL, job.sequence, job.fenceEntity, job.fenceSource, job.termID,
		job.kind, job.library, job.subject, job.text, job.sequence)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, err
	}
	if n == 0 {
		return false, nil
	}
	job.passSequence = job.sequence
	job.phase = 0
	job.cursorEntity, job.cursorSource = 0, visibilitySourceMemberNoCursor
	return true, nil
}

func stepVisibilitySourceJob(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob, budget int) (int, error) {
	if job.passSequence == 0 {
		ok, err := beginVisibilitySourcePass(ctx, tx, job)
		if err != nil || !ok {
			return 0, err
		}
	}
	if job.kind == visibilitySourceKindItem && job.phase == 0 {
		// The item's own browse row is the container-invalidation arm the
		// ordinary attribute/availability triggers cannot reach: an edge written
		// on an entity that is itself a browse row must dirty that row too. One
		// indexed probe, charged to no raw budget.
		if _, err := tx.ExecContext(ctx, visibilityChildRowJobSQL, job.subject, job.passSequence, job.firstPending); err != nil {
			return 0, err
		}
		job.phase = 1
		job.cursorEntity, job.cursorSource = 0, visibilitySourceMemberNoCursor
		if err := checkpointVisibilitySourcePass(ctx, tx, job); err != nil {
			return 0, err
		}
	}
	switch job.kind {
	case visibilitySourceKindRow:
		return stepVisibilityRowSourceFanout(ctx, tx, job, budget)
	case visibilitySourceKindItem, visibilitySourceKindContainer:
		return stepVisibilityMemberSourceFanout(ctx, tx, job, budget)
	case visibilitySourceKindRatingAge:
		return stepVisibilityRatingAgeSourceFanout(ctx, tx, job, budget)
	}
	return 0, fmt.Errorf("visibility source job kind %d", job.kind)
}

// stepVisibilityRowSourceFanout reads at most budget class ids up to the
// captured high-water class. A retired class is a charged rejection: it is read
// and abandoned, but no dirty row is written for it.
func stepVisibilityRowSourceFanout(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob, budget int) (int, error) {
	rows, err := tx.QueryContext(ctx, visibilityClassPageSQL, job.library, job.cursorEntity, job.fenceEntity, budget)
	if err != nil {
		return 0, err
	}
	type visibilityClass struct{ id, retired int64 }
	page := make([]visibilityClass, 0, budget)
	for rows.Next() {
		var c visibilityClass
		if err = rows.Scan(&c.id, &c.retired); err != nil {
			rows.Close()
			return 0, err
		}
		page = append(page, c)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, c := range page {
		if c.retired != 0 {
			continue
		}
		if _, err = tx.ExecContext(ctx, upsertVisibilityDirtySQL, c.id, job.subject, job.firstPending); err != nil {
			return 0, err
		}
	}
	if len(page) < budget {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	last := page[len(page)-1]
	if last.id >= job.fenceEntity {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	job.cursorEntity = last.id
	job.cursorSource = visibilitySourceMemberNoCursor
	return len(page), checkpointVisibilitySourcePass(ctx, tx, job)
}

// stepVisibilityMemberSourceFanout walks the complete (entity_id,source) tuple.
// The point container probe runs after the raw page, so every tuple is charged
// and the cursor advances even when the container has no row (kind 2) or is
// not a show/season (kind 3).
func stepVisibilityMemberSourceFanout(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob, budget int) (int, error) {
	rows, err := tx.QueryContext(ctx, visibilityMemberPageSQL, job.subject, job.cursorEntity, job.cursorSource,
		job.fenceEntity, job.fenceSource, budget)
	if err != nil {
		return 0, err
	}
	type visibilityMember struct{ entity, source int64 }
	page := make([]visibilityMember, 0, budget)
	for rows.Next() {
		var m visibilityMember
		if err = rows.Scan(&m.entity, &m.source); err != nil {
			rows.Close()
			return 0, err
		}
		page = append(page, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	child := visibilityChildRowJobSQL
	if job.kind == visibilitySourceKindContainer {
		child = visibilityChildContainerJobSQL
	}
	for _, m := range page {
		// A missing container (or a non show/season one for kind 3) inserts no
		// child row, which is the point rejection. The raw tuple is still spent.
		if _, err = tx.ExecContext(ctx, child, m.entity, job.passSequence, job.firstPending); err != nil {
			return 0, err
		}
	}
	if len(page) < budget {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	last := page[len(page)-1]
	if last.entity == job.fenceEntity && last.source == job.fenceSource {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	job.cursorEntity, job.cursorSource = last.entity, last.source
	return len(page), checkpointVisibilitySourcePass(ctx, tx, job)
}

// stepVisibilityRatingAgeSourceFanout walks the raw item edges of the term
// captured at pass start. The term was resolved once by its unique
// (field,value_key) key; no join or cross-term ordering happens per page.
func stepVisibilityRatingAgeSourceFanout(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob, budget int) (int, error) {
	rows, err := tx.QueryContext(ctx, visibilityRatingAgeItemPageSQL, job.termID, job.cursorEntity, job.fenceEntity, budget)
	if err != nil {
		return 0, err
	}
	page := make([]int64, 0, budget)
	for rows.Next() {
		var item int64
		if err = rows.Scan(&item); err != nil {
			rows.Close()
			return 0, err
		}
		page = append(page, item)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return 0, err
	}
	for _, item := range page {
		if _, err = tx.ExecContext(ctx, visibilityChildItemJobSQL, item, job.passSequence, job.firstPending); err != nil {
			return 0, err
		}
	}
	if len(page) < budget {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	last := page[len(page)-1]
	if last >= job.fenceEntity {
		return len(page), finishVisibilitySourcePass(ctx, tx, job)
	}
	job.cursorEntity = last
	job.cursorSource = visibilitySourceMemberNoCursor
	return len(page), checkpointVisibilitySourcePass(ctx, tx, job)
}

func checkpointVisibilitySourcePass(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob) error {
	_, err := tx.ExecContext(ctx, checkpointVisibilitySourcePassSQL, job.phase, job.cursorEntity, job.cursorSource,
		job.kind, job.library, job.subject, job.text, job.passSequence)
	return err
}

// finishVisibilitySourcePass acknowledges exactly the frontier the pass
// captured. With no write during the pass the key is deleted. Otherwise the
// earliest such event becomes the new first_pending and the next pass restarts
// from the beginning: rows appended above the old fence and rows rewritten
// behind the cursor are both covered, and the latest sequence is retained.
func finishVisibilitySourcePass(ctx context.Context, tx *sql.Tx, job *visibilitySourceJob) error {
	res, err := tx.ExecContext(ctx, deleteFinishedVisibilitySourcePassSQL,
		job.kind, job.library, job.subject, job.text, job.passSequence, job.sequence)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		return nil
	}
	res, err = tx.ExecContext(ctx, rerunVisibilitySourcePassSQL,
		job.kind, job.library, job.subject, job.text, job.passSequence)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n > 0 {
		return nil
	}
	_, err = tx.ExecContext(ctx, rereadVisibilitySourcePassSQL, job.passSequence+1,
		job.kind, job.library, job.subject, job.text, job.passSequence, job.passSequence)
	return err
}

// VisibilitySourceTargetReadyTx reports whether every source event up to target
// has been covered by a finished pass. It is the exact indexed NOT EXISTS behind
// class source targets and facet fences; later events are not consulted, so
// continuous churn cannot hold a captured target open forever.
func VisibilitySourceTargetReadyTx(ctx context.Context, tx *sql.Tx, target int64) (bool, error) {
	var ready bool
	err := tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM compact_visibility_source_jobs WHERE first_pending<=?)`, target).Scan(&ready)
	return ready, err
}

// VisibilitySourceClock reads the monotone visibility source sequence. A
// generation or class captures this once at begin, and readiness then compares
// that fixed target through VisibilitySourceTargetReadyTx.
func VisibilitySourceClock(ctx context.Context, q ReadQuery) (int64, error) {
	var sequence int64
	err := q.QueryRowContext(ctx, `SELECT sequence FROM compact_visibility_source_state WHERE id=1`).Scan(&sequence)
	return sequence, err
}
