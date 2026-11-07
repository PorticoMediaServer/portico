package writer

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"math"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func uoPersonalFixture(t *testing.T) (*executor, uoPersonalIntentInput) {
	t.Helper()
	w, f := uoFlagsFixture(t)
	q := uoPersonalIntentInput{policy: uoPersonalPolicy{profile: f.profileKey, authority: "local", account: "account-one", profileID: "profile-one", memberBirth: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", profileBirth: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", historyPolicy: 1}, subjectID: f.subjectID, libraryID: f.libraryID, entityRevision: 1, admissionRevision: 1, publicID: f.publicID, library: f.libraryKey, activityGeneration: 1, at: "2026-11-07T00:00:00Z", watched: true}
	wc008NativeExec(t, w, `INSERT INTO catalog_membership_clock(singleton,sequence) VALUES(1,10)`)
	return w, q
}
func uoPersonalAllDefinitions() []stmt.Definition {
	var out []stmt.Definition
	seen := map[stmt.ID]bool{}
	for _, group := range [][]stmt.Definition{uoPersonalIntentDefinitions(), uoPersonalFlagsDefinitions(), uoPersonalHistoryDefinitions(), uoPersonalOfflineDefinitions(), uoPersonalBulkDefinitions()} {
		for _, d := range group {
			if !seen[d.ID] {
				out = append(out, d)
				seen[d.ID] = true
			}
		}
	}
	return out
}

// COMPONENT ONLY: synthetic existing ProgressAccept custody; no UO command
// registration, authority/replay/receipt/quota or physical qualification claim.
func uoPersonalTurn(t *testing.T, w *executor) *commandTurn {
	t.Helper()
	return wc008NativeTurn(t, w, ProgressAccept, uoPersonalAllDefinitions())
}
func uoPersonalRollback(t *testing.T, turn *commandTurn) {
	t.Helper()
	if _, e := turn.transaction.rollback(nil); e != nil {
		t.Fatal(e)
	}
}
func uoPersonalContainer(t *testing.T, w *executor) {
	t.Helper()
	wc008NativeExec(t, w, `INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(2,'show',0,1,1,1,0)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(2112,x'03800000000000000000000000000001',7,2)`)
	wc008NativeExec(t, w, `INSERT INTO catalog_entities(id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,random_key,metadata_incarnation,artwork_incarnation) SELECT id,public_id,library_id,kind,'Show','show','s','show','s',0,public_id,public_id FROM catalog_subjects WHERE id=2112`)
	wc008NativeExec(t, w, `INSERT INTO catalog_scalar_contributions(library_id,parent_id,contributor_id,source,contributor_revision,membership_sequence) VALUES(7,2112,2111,0,1,5),(7,2112,2111,1,1,5)`)
}
func TestUOPersonalIntentWatchedContainerAndDismiss(t *testing.T) {
	w, q := uoPersonalFixture(t)
	uoPersonalContainer(t, w)
	turn := uoPersonalTurn(t, w)
	e, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil || e.revision != 1 || e.action != 1 || !e.watched {
		t.Fatal(e, err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE personal_items SET resume_playback_id='playing-one',resume_item_public_id=item_public_id,resume_position=42,play_count=5,played_ms=1000,watchlisted=1 WHERE item_id=2111`)
	parent := q
	parent.subjectID = 2112
	parent.publicID[15] = 1
	parent.container = true
	turn = uoPersonalTurn(t, w)
	p, err := applyUOPersonalIntent(t.Context(), turn, parent)
	if err != nil || p.membership != 10 || p.action != 2 {
		t.Fatal(p, err)
	}
	uoFlagsCommit(t, turn)
	var count, plays, position int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT count(*),play_count,resume_position FROM personal_items WHERE item_id=2111`).Scan(&count, &plays, &position); err != nil || count != 1 || plays != 5 || position != 42 {
		t.Fatal("container rewrote descendant", count, plays, position, err)
	}
	q.expectedRevision = 1
	q.watched = false
	turn = uoPersonalTurn(t, w)
	u, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil || u.action != 3 || u.watched {
		t.Fatal(u, err)
	}
	uoFlagsCommit(t, turn)
	var fence string
	if err = w.db.QueryRowContext(t.Context(), `SELECT evidence_fence_playback_id FROM personal_items WHERE item_id=2111`).Scan(&fence); err != nil || fence != "playing-one" {
		t.Fatal("explicit intent failed to fence current playback", fence, err)
	}
	var clearedWatchTime bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT watched_at IS NULL FROM personal_items WHERE item_id=2111`).Scan(&clearedWatchTime); err != nil || !clearedWatchTime {
		t.Fatal("unwatch retained watched timestamp", clearedWatchTime, err)
	}
	q.expectedRevision = 2
	q.dismiss = true
	turn = uoPersonalTurn(t, w)
	d, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil || !d.dismissed || d.action != 4 {
		t.Fatal(d, err)
	}
	uoFlagsCommit(t, turn)
	var dismissed string
	var watched, intent, saved, history int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT dismissed_playback_id,watched,container_intent_sequence,watchlisted,(SELECT count(*) FROM play_history) FROM personal_items WHERE item_id=2111`).Scan(&dismissed, &watched, &intent, &saved, &history); err != nil || dismissed != "playing-one" || watched != 0 || intent != 3 || saved != 1 || history != 0 {
		t.Fatal("dismiss rewrote watched/saved/history", dismissed, watched, intent, saved, history, err)
	}
}
func TestUOPersonalIntentRollbackLifetimeAndStaleGeneration(t *testing.T) {
	for _, why := range []string{"viewer-overflow", "identity", "generation", "policy", "canceled", "revision"} {
		t.Run(why, func(t *testing.T) {
			w, q := uoPersonalFixture(t)
			switch why {
			case "viewer-overflow":
				wc008NativeExec(t, w, `INSERT INTO viewer_revisions(profile_id,library_id,revision) VALUES('profile-one','library-one',?)`, int64(math.MaxInt64))
			case "identity":
				q.publicID[15] = 9
			case "generation":
				q.activityGeneration = 2
			case "policy":
				q.policy.historyPolicy = 0
			case "revision":
				q.expectedRevision = 1
			}
			turn := uoPersonalTurn(t, w)
			ctx := t.Context()
			if why == "canceled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			e, err := applyUOPersonalIntent(ctx, turn, q)
			if err == nil || e != (uoPersonalIntentEffect{}) {
				t.Fatal("error leaked effect", e, err)
			}
			uoPersonalRollback(t, turn)
			var count int64
			if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM personal_items)+(SELECT count(*) FROM personal_activity_revisions)`).Scan(&count); err != nil || count != 0 {
				t.Fatal("rollback leaked prefix", count, err)
			}
		})
	}
	if e, err := applyUOPersonalIntent(t.Context(), nil, uoPersonalIntentInput{}); err == nil || e != (uoPersonalIntentEffect{}) {
		t.Fatal(e, err)
	}
}
func TestUOPersonalDraftDefinitionsPrepare(t *testing.T) {
	w, _ := uoPersonalFixture(t)
	wc008NativePrepare(t, w, uoPersonalAllDefinitions())
}
func TestUOPersonalDraftFinishGuards(t *testing.T) {
	want := map[string]bool{"applyUOPersonalIntent": false, "applyUOPersonalClear": false, "applyUOPersonalBoundary": false, "applyUOPersonalOffline": false, "applyUOPersonalResolve": false, "applyUOPersonalBulkBegin": false, "applyUOPersonalBulkStep": false, "applyUOPersonalBulkMembers": false}
	for _, path := range []string{"uo_personal_intent.go", "uo_personal_history.go", "uo_personal_offline.go", "uo_personal_bulk.go"} {
		f, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok {
				continue
			}
			if _, ok = want[fn.Name.Name]; !ok {
				continue
			}
			guard, ok := fn.Body.List[0].(*ast.DeferStmt)
			if !ok || !guardedFinishDefer(fn, guard) {
				t.Fatal("missing shared finish", fn.Name.Name)
			}
			want[fn.Name.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Fatal("missing native effect", name)
		}
	}
}
func TestUOPersonalScalarCodec(t *testing.T) {
	for _, v := range []string{"00", "-1", "11", "1.0", "true", "{}", "NULL"} {
		if uoPersonalRating.validValue(v) {
			t.Fatal("noncanonical rating", v)
		}
	}
	if !uoPersonalRating.validValue("null") || !uoPersonalRating.validValue("0") || uoPersonalFavorite.validValue("null") {
		t.Fatal("scalar semantics")
	}
}

func TestUOPersonalContainerUndoRestoresOriginalOrderAndWatermark(t *testing.T) {
	w, q := uoPersonalFixture(t)
	uoPersonalContainer(t, w)
	q.subjectID = 2112
	q.publicID[15] = 1
	q.container = true
	turn := uoPersonalTurn(t, w)
	first, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	wc008NativeExec(t, w, `UPDATE catalog_membership_clock SET sequence=20`)
	q.expectedRevision = 1
	q.watched = false
	turn = uoPersonalTurn(t, w)
	second, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil || second.membership != 20 {
		t.Fatal(second, err)
	}
	uoFlagsCommit(t, turn)
	q.expectedRevision = 2
	q.watched = true
	q.restore = uoPersonalIntentRestore{set: true, membership: first.membership, intent: first.intent, generation: 1}
	turn = uoPersonalTurn(t, w)
	undo, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err != nil || undo.action <= second.action || undo.intent != first.intent || undo.membership != 10 {
		t.Fatal(undo, err)
	}
	uoFlagsCommit(t, turn)
	var sequence, watermark, action int64
	if err = w.db.QueryRowContext(t.Context(), `SELECT action_sequence,container_intent_sequence,container_membership_sequence FROM personal_items WHERE item_id=2112`).Scan(&action, &sequence, &watermark); err != nil || action != undo.action || sequence != first.intent || watermark != first.membership {
		t.Fatal(action, sequence, watermark, err)
	}
	turn = uoPersonalTurn(t, w)
	_, err = applyUOPersonalClear(t.Context(), turn, uoPersonalClearInput{q.policy, uoClearActivity, 3})
	if err != nil {
		t.Fatal(err)
	}
	uoFlagsCommit(t, turn)
	q.activityGeneration = 2
	q.expectedRevision = 3
	turn = uoPersonalTurn(t, w)
	bad, err := applyUOPersonalIntent(t.Context(), turn, q)
	if err == nil || bad != (uoPersonalIntentEffect{}) {
		t.Fatal("undo resurrected cleared generation", bad, err)
	}
	uoPersonalRollback(t, turn)
}
