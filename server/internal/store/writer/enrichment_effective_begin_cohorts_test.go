package writer

import "testing"

func TestEffectiveBeginExactOwnerCohortMatchesNativeFixture(t *testing.T) {
	w, _, request, identity := effectiveBeginWholeFixture(t)
	input, err := newEffectiveStageBeginCommand(identity, request)
	if err != nil {
		t.Fatal(err)
	}
	turn, steps := effectiveBeginWholeTurn(t, w)
	image, err := effectiveBeginInsertHiddenHeader(t.Context(), turn, steps[enrichmentEffectiveInsert], request.header, input.digest, input.retirementIncarnation)
	if err != nil {
		t.Fatal(err)
	}
	// Static cohort result coordinates have an actual same-entity target, not a
	// nonexistent FK or a substituted runtime binding. Preparation is fixture-only.
	if _, err := turn.transaction.conn.ExecContext(t.Context(), "UPDATE catalog_credit_images SET id=307,revision=409 WHERE id=?", image.id); err != nil {
		t.Fatal(err)
	}
	var bindings []any
	for _, definition := range enrichmentEffectiveBeginDefinitions() {
		if definition.ID == enrichmentEffectiveAttach {
			if len(definition.Cohorts) != 1 || definition.Cohorts[0].Name != "exact_owner_cas" {
				t.Fatal("exact cohort changed")
			}
			bindings = definition.Cohorts[0].Bindings
		}
	}
	if len(bindings) != 14 {
		t.Fatal("missing exact owner cohort")
	}
	result, err := turn.exec(t.Context(), steps[enrichmentEffectiveAttach], bindings...)
	if err != nil {
		t.Fatal(err)
	}
	n, err := result.RowsAffected()
	if err != nil || n != 1 {
		t.Fatalf("purported exact owner cohort missed native fixture: rows=%d err=%v", n, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
}
