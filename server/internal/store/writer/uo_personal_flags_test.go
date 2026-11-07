package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"testing"
)

func uoFlagsFixture(t *testing.T) (*executor, uoPersonalFlagsInput) {
	t.Helper()
	w := wc008NativeExecutor(t)
	q := uoPersonalFlagsInput{profileKey: "profile-one", libraryKey: "library-one", subjectID: 2111, libraryID: 7, publicID: [16]byte{3, 128}, entityRevision: 1, admissionRevision: 1,
		patch: uoPersonalFlagsPatch{watchlisted: uoFlagValue{set: true, value: true}}}
	wc008NativeExec(t, w, `INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library-one','/fixture')`)
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2111,?,7,1)`, q.publicID[:])
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) VALUES(2111,?,7,1,'Title','title','t','title','t',0,?,?)`, q.publicID[:], q.publicID[:], q.publicID[:])
	return w, q
}

// Component execution uses the existing test turn's synthetic custody. It does
// not register/qualify a UO command or exercise the independent authority/receipt
// dispatcher. These are native body/rollback controls on the canonical schema.
func uoFlagsTurn(t *testing.T, w *executor) *commandTurn {
	return wc008NativeTurn(t, w, ProgressAccept, uoPersonalFlagsDefinitions())
}

func uoFlagsCommit(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, err := turn.transaction.commit(); err != nil {
		t.Fatal(err)
	}
}

func TestUOFlagsSparseInsertPatchAndNoop(t *testing.T) {
	w, q := uoFlagsFixture(t)
	turn := uoFlagsTurn(t, w)
	first, err := applyUOPersonalFlags(t.Context(), turn, q)
	if err != nil || !first.changed || !first.state.watchlisted || first.state.revision != 1 || first.viewerRevision != 1 || first.selectionRevision != 1 {
		t.Fatal("first sparse state", first, err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE personal_items SET resume_position=42,watched=1,play_count=3,played_ms=1234 WHERE profile_id='profile-one'`)
	q.expectedRevision = 1
	q.patch = uoPersonalFlagsPatch{rating: uoRatingValue{set: true, value: sql.NullInt64{Int64: 0, Valid: true}}}
	turn = uoFlagsTurn(t, w)
	second, err := applyUOPersonalFlags(t.Context(), turn, q)
	if err != nil || !second.changed || !second.state.watchlisted || !second.state.rating.Valid || second.state.rating.Int64 != 0 || second.state.revision != 2 || second.viewerRevision != 2 || second.selectionRevision != 2 {
		t.Fatal("zero rating patch", second, err)
	}
	uoFlagsCommit(t, turn)
	var position, watched, plays, played, eventRows, historyRows int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT resume_position,watched,play_count,played_ms,(SELECT count(*) FROM api_events),(SELECT count(*) FROM play_history) FROM personal_items WHERE profile_id='profile-one'`).Scan(&position, &watched, &plays, &played, &eventRows, &historyRows); err != nil || position != 42 || watched != 1 || plays != 3 || played != 1234 || eventRows != 0 || historyRows != 0 {
		t.Fatal("flags rewrote activity or emitted uncomposed events", position, watched, plays, played, eventRows, historyRows, err)
	}
	q.expectedRevision = 2
	turn = uoFlagsTurn(t, w)
	var before, after int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	noop, err := applyUOPersonalFlags(t.Context(), turn, q)
	if err != nil || noop.changed || noop.state != second.state {
		t.Fatal("no-op state", noop, err)
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("no-op mutated native state", before, after, err)
	}
	uoFlagsCommit(t, turn)
	q.patch.rating.value = sql.NullInt64{}
	turn = uoFlagsTurn(t, w)
	cleared, err := applyUOPersonalFlags(t.Context(), turn, q)
	if err != nil || !cleared.changed || cleared.state.rating.Valid || cleared.state.revision != 3 {
		t.Fatal("clear rating", cleared, err)
	}
	uoFlagsCommit(t, turn)
}

func TestUOFlagsDefaultPatchRemainsSparse(t *testing.T) {
	w, q := uoFlagsFixture(t)
	q.patch.watchlisted.value = false
	turn := uoFlagsTurn(t, w)
	effect, err := applyUOPersonalFlags(t.Context(), turn, q)
	if err != nil || effect.changed || effect.state != (uoPersonalFlagsState{}) {
		t.Fatal(effect, err)
	}
	uoFlagsCommit(t, turn)
	var rows int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM personal_items)+(SELECT count(*) FROM viewer_revisions)`).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("default patch materialized personal state", rows, err)
	}
}

func TestUOFlagsCASAndRollback(t *testing.T) {
	for _, failure := range []string{"source", "personal", "viewer-overflow", "selection-overflow", "canceled"} {
		t.Run(failure, func(t *testing.T) {
			w, q := uoFlagsFixture(t)
			switch failure {
			case "source":
				wc008NativeExec(t, w, `UPDATE catalog_entities SET revision=2 WHERE id=2111`)
			case "personal":
				q.expectedRevision = 1
			case "viewer-overflow":
				wc008NativeExec(t, w, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('profile-one','library-one',?)`, int64(math.MaxInt64))
			case "selection-overflow":
				wc008NativeExec(t, w, `INSERT INTO viewer_revisions(profile_id,library_id,revision,selection_revision) VALUES('profile-one','library-one',1,?)`, int64(math.MaxInt64))
			}
			turn := uoFlagsTurn(t, w)
			ctx := t.Context()
			if failure == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			effect, err := applyUOPersonalFlags(ctx, turn, q)
			if err == nil || effect != (uoPersonalFlagsEffect{}) {
				t.Fatal("failed component leaked prefix", effect, err)
			}
			if _, err := turn.transaction.rollback(nil); err != nil {
				t.Fatal(err)
			}
			var rows int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM personal_items`).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("failed head left personal prefix", rows, err)
			}
		})
	}
}

func TestUOFlagsInvalidPatchAndMissingTurn(t *testing.T) {
	_, base := uoFlagsFixture(t)
	for _, edit := range []func(*uoPersonalFlagsInput){
		func(q *uoPersonalFlagsInput) { q.patch = uoPersonalFlagsPatch{} },
		func(q *uoPersonalFlagsInput) { q.patch.favorite.value = true },
		func(q *uoPersonalFlagsInput) {
			q.patch.rating = uoRatingValue{set: true, value: sql.NullInt64{Int64: 11, Valid: true}}
		},
		func(q *uoPersonalFlagsInput) {
			q.patch.rating = uoRatingValue{set: true, value: sql.NullInt64{Int64: 1}}
		},
	} {
		q := base
		edit(&q)
		if !errors.Is(q.validate(), ErrInvalid) {
			t.Fatal("invalid patch accepted", q)
		}
	}
	if effect, err := applyUOPersonalFlags(t.Context(), nil, base); err == nil || effect != (uoPersonalFlagsEffect{}) {
		t.Fatal("component ran without W turn", effect, err)
	}
}
