package writer

import (
	"errors"
	"math"
	"reflect"
	"testing"
)

func TestInventoryLookupDiscriminatesLifecycleBeforeNullableRevision(t *testing.T) {
	w, requests, registry := inventoryLookupFixture(t)
	for _, kind := range []string{"inventory_create", "inventory_claim"} {
		t.Run(kind, func(t *testing.T) {
			turn, tx := inventoryLookupTurn(t, w, registry)
			owner, generation, expiry := int64(0), int64(0), int64(0)
			if kind == "inventory_claim" {
				owner, generation, expiry = 1, 1, math.MaxInt64
			}
			// Actual adopted010a nullable/kind CHECKs stay enabled. The N1 caller
			// must recognize this as another purpose before any cohort decoding.
			if _, err := tx.conn.ExecContext(tx.ctx, `UPDATE scan_command_receipts SET
command_kind=?,source_revision=NULL,accepted_revision=1,result_count=0,next_cursor='',
result_lease_owner=?,result_lease_generation=?,result_lease_expires_ms=?,
result_acknowledged_sequence=0,result_discovered=0,result_processed=0,
result_root_identity='native-root',result_root_revision='native-revision',
result_root_cursor='',result_root_pages=0,result_root_skipped=0 WHERE run_id='scan-hit'`, kind, owner, generation, expiry); err != nil {
				t.Fatal(err)
			}
			got, err := lookupInventoryAccept(t.Context(), turn, requests["hit"])
			if !errors.Is(err, ErrConflict) || !reflect.DeepEqual(got, inventoryReceiptLookup{}) {
				t.Fatal("nullable lifecycle entered N1 decoding", got, err)
			}
			tx.rollback(err)
		})
	}
}
