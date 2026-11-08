package writer

import (
	"testing"
	"testing/synctest"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// TestVerifiedEffectiveBeginNativeGuardsRejectMutations drives the same native
// E/S stage/seal lineage and governor-backed dispatcher as the verified BEGIN
// positive control, then breaks one persisted source fact immediately before
// the fresh command. Every mutation must refuse and leave no durable BEGIN.
func TestVerifiedEffectiveBeginNativeGuardsRejectMutations(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate string
		pins   int64
	}{
		{
			name:   "source revision advanced",
			mutate: `UPDATE metadata_provider_operations SET source_content_revision=source_content_revision+1 WHERE id='operation-one'`,
			pins:   1,
		},
		{
			name:   "proof reservation cleared",
			mutate: `UPDATE metadata_provider_operations SET proof_pin_units=0,proof_reserved_bytes=0 WHERE id='operation-one'`,
			pins:   1,
		},
		{
			name:   "source seal pins cleared",
			mutate: `UPDATE enrichment_command_receipts SET proof_pin_count=0 WHERE command_kind='credit_source_seal'`,
			pins:   0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				e, c, f := verifiedEffectiveRequest(t)
				w, cap, a := verifiedEffectiveRuntime(t, e, c)
				if _, err := e.db.ExecContext(t.Context(), tc.mutate); err != nil {
					t.Fatal(err)
				}
				got, err := w.StageEffective(t.Context(), cap, c)
				if err == nil || got != (EffectiveStageResult{}) {
					t.Fatal("mutated source accepted", got, err)
				}
				var images, receipts, pins int64
				if err = e.db.QueryRowContext(t.Context(), `SELECT (SELECT count(*) FROM catalog_credit_images),(SELECT count(*) FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?),(SELECT proof_pin_count FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?)`,
					c.request.ownerID, c.identity.Incarnation[:], c.identity.Sequence,
					f.scope.ownerID, f.sourceReceipt.Identity.Incarnation[:], f.sourceReceipt.Identity.Sequence).Scan(&images, &receipts, &pins); err != nil {
					t.Fatal(err)
				}
				if images != 0 || receipts != 0 || pins != tc.pins {
					t.Fatal("partial fresh effect", images, receipts, pins)
				}
				if a.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
					t.Fatal("custody not returned", a.Usage(), e.db.Stats())
				}
			})
		})
	}
}
