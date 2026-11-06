package writer

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func sealEffectFixture(t *testing.T) (*executor, sealOwnerScope) {
	t.Helper()
	w, _, identity := evidenceBuildPinFixture(t)
	var entityInc [16]byte
	for i := range entityInc {
		entityInc[i] = byte(48 + i)
	}
	scope := sealOwnerScope{ownerID: "operation-one", provider: "fixture", lease: "native-lease", leaseUntil: "2100-11-05T12:00:00Z", afterItem: "opaque-page:61", selectedID: "opaque:selection", identity: identity,
		generation: 31, last: 11, ack: 11, entityID: 101, entityRevision: 203, page: 59, sourceRevision: 1, entityIncarnation: entityInc,
		selection: effectiveBeginSelection{Incarnation: [16]byte{71}, Revision: 73, Role: "primary", Kind: "movie", ID: "opaque:selection", Status: "selected"}, selectionObserved: "2026-11-06T12:00:00Z"}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_subject_providers(entity_id,provider,role,incarnation,revision,subject_kind,provider_id,status,observed_at)
 VALUES(101,'fixture','primary',?,73,'movie','opaque:selection','selected',?)`, scope.selection.Incarnation[:], scope.selectionObserved); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET evidence_build_id='build-original:211' WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	return w, scope
}
func sealEffectTurn(t *testing.T, w *executor, defs []stmt.Definition) *commandTurn {
	return sealEffectTurnPurpose(t, w, defs, CreditStage)
}
func sealEffectTurnPurpose(t *testing.T, w *executor, defs []stmt.Definition, kind CommandID) *commandTurn {
	t.Helper()
	// SQL/body component only. W must select real seal purposes and complete
	// measured production recipes; CreditStage here grants no seal authority.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), kind, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	registry, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{64, 20000, 128, 65536, time.Millisecond}})
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn
}
func populateSealCandidates(t *testing.T, w *executor) {
	t.Helper()
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_candidates(id,operation_id,ordinal,entity_id,provider,subject_kind,provider_id,evidence_id,title,artist,edition,confidence,decision,query_digest,input_digest,strong_signals,contradiction,source_kind,observed_at,language,region)
 VALUES('candidate-one','operation-one',0,101,'fixture','movie','opaque:selection','evidence-original:197','Title','','',1,'selected','q','i',1,0,'fixture','2026-11-06T12:00:00Z','en-CA','CA')`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 17; i++ {
		for _, q := range []string{`INSERT INTO metadata_candidate_reasons VALUES('candidate-one',?,'reason')`, `INSERT INTO metadata_candidate_aliases VALUES('candidate-one',?,'alias')`} {
			if _, err := w.db.ExecContext(t.Context(), q, i); err != nil {
				t.Fatal(err)
			}
		}
	}
}
func TestSealCandidateRetirementBoundedChildFirstAndRollback(t *testing.T) {
	w, scope := sealEffectFixture(t)
	populateSealCandidates(t, w)
	turn := sealEffectTurn(t, w, enrichmentCandidateRetirementDefinitions())
	got, err := retireSealCandidates(t.Context(), turn, scope)
	if err != nil || got.reasons != 16 || got.aliases != 16 || got.candidates != 0 || got.revision != 2 || got.complete {
		t.Fatal("first quantum", got, err)
	}
	scope.sourceRevision = got.revision
	got, err = retireSealCandidates(t.Context(), turn, scope)
	if err != nil || got.reasons != 1 || got.aliases != 1 || got.candidates != 1 || got.revision != 3 || !got.complete || got.removedEvidence != (sql.NullString{String: "evidence-original:197", Valid: true}) {
		t.Fatal("final child-before-parent quantum", got, err)
	}
	// Zero retained candidates is conditional on the native primary selection,
	// never inferred from removal of a selected candidate or a supplied flag.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), `UPDATE metadata_subject_providers SET status='stale'`); err != nil {
		t.Fatal(err)
	}
	scope.sourceRevision = got.revision
	if _, err := retireSealCandidates(t.Context(), turn, scope); !errors.Is(err, ErrConflict) {
		t.Fatal("lost current binding accepted", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM metadata_candidate_reasons`).Scan(&count); err != nil || count != 17 {
		t.Fatal("cleanup escaped rollback", count, err)
	}
}
