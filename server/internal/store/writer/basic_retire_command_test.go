package writer

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestBasicRetireClosedCarrierKeepsEpochsDistinct(t *testing.T) {
	in, identity := basicStageFactCommandFixture()
	old := in.fact
	in = basicStageRequest{original: in.original, purpose: BasicPurposeStage, operation: basicStageRetireInput, retire: basicRetireFirstEffects{source: old, currentAssetEpoch: 18, changeRevision: 15, shapeID: 61, entityID: 31, counts: [9]int64{1}}}
	c, err := newBasicStageCommand(identity, in)
	if err != nil || c == nil || c.request.retire.source.expectedEpoch != 17 || c.request.retire.currentAssetEpoch != 18 {
		t.Fatal(c, err)
	}
	in.retire.counts[0] = 7
	if c.request.retire.counts[0] != 1 {
		t.Fatal("caller mutated frozen manifest")
	}
	q, err := c.receiptRequest()
	if err != nil || q.Purpose != BasicPurposeStage || q.Scan.Receipt.RequestDigest != c.digest {
		t.Fatal(q, err)
	}
	in = c.request
	in.operation = basicStageFactInput
	if c, err := newBasicStageCommand(identity, in); c != nil || !errors.Is(err, ErrInvalid) {
		t.Fatal("retirement payload borrowed another operation", c, err)
	}
	for _, d := range basicRetireFirstDefinitions() {
		want := 1
		if d.ID == basicRetireDeleteFamily {
			want = 9
		}
		if basicStageCommandMaxUses(basicStageRetireInput, d.ID) != want || basicEffectsMaxUses(BasicStage, d.ID) != want || basicEffectsMaxUses(BasicSeal, d.ID) != 0 {
			t.Fatal("retirement use/purpose mismatch", d.ID)
		}
	}
	if basicStageCommandMaxUses(basicStageRetireInput, stmt.ID("scan.basic_retire.unknown")) != 0 {
		t.Fatal("open retirement inventory")
	}
}

func TestBasicRetireHandlerSavesDeletedEpochAndHistoricalProof(t *testing.T) {
	w, s, original, identity := basicControlFixture(t)
	in, charge := seedBasicRetiredFirstInput(t, w, s)
	// The existing input's immutable witness remains its provenance. Cleanup
	// does not borrow a new object's revision or require the old object live.
	if _, err := w.db.ExecContext(t.Context(), `DELETE FROM inventory_objects WHERE id='basic-object'`); err != nil {
		t.Fatal(err)
	}
	c, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageRetireInput, retire: in})
	if err != nil {
		t.Fatal(err)
	}
	defs := basicCommandDefinitions(BasicStage)
	registry := nativeInventoryFixtureRegistry(t, w, defs, "basic_retire_command_test.go", "basic_retire_first_test.go")
	turn := basicHandlerTurn(t, w, BasicStage, defs, registry)
	got, err := applyBasicStage(t.Context(), turn, c)
	if err != nil || got.Outcome != (ReceiptOutcome{}) || got.AcceptedRunRevision != 42 || got.ChangeID != 51 || got.ExpectedEpoch != 17 || got.ResultCount != 0 || got.Members == nil || len(got.Members) != 0 || got.Continuation != (BasicContinuation{}) {
		t.Fatal(got, err)
	}
	basicMediaCommit(t, turn)
	var exact bool
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT info_epoch=18 FROM catalog_assets WHERE id=41) AND (SELECT count(*)=0 FROM catalog_info_changes WHERE id=51) AND (SELECT used_bytes=777 FROM catalog_info_input_budget) AND (SELECT expected_epoch=17 AND accepted_revision=42 FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=19)`).Scan(&exact); err != nil || !exact {
		t.Fatal(exact, charge, err)
	}
	if _, err = w.db.ExecContext(t.Context(), `UPDATE inventory_runs SET job_revision=9223372036854775807,job_lease_expires_ms=0 WHERE job_id='basic-run'`); err != nil {
		t.Fatal(err)
	}
	q, err := c.receiptRequest()
	if err != nil {
		t.Fatal(err)
	}
	history := basicHandlerTurn(t, w, BasicStage, defs, registry)
	lookup, err := lookupBasic(t.Context(), history, q)
	if err != nil || lookup.decision != receiptReplay || lookup.result.ExpectedEpoch != 17 || lookup.result.AcceptedRunRevision != 42 || lookup.result.Digest != got.Digest {
		t.Fatal(lookup, err)
	}
	if floor, err := acknowledgeBasic(t.Context(), history, q, lookup); err != nil || floor != 19 {
		t.Fatal(floor, err)
	}
	basicMediaCommit(t, history)
	if err = w.db.QueryRowContext(t.Context(), `SELECT (SELECT used_bytes=777 FROM catalog_info_input_budget) AND (SELECT count(*)=1 FROM scan_command_receipts WHERE run_id='basic-run' AND command_sequence=19) AND (SELECT acknowledged_command_sequence=19 AND job_revision=9223372036854775807 FROM inventory_runs WHERE job_id='basic-run')`).Scan(&exact); err != nil || !exact {
		t.Fatal("ACK refunded debt or discarded proof", exact, err)
	}
}
