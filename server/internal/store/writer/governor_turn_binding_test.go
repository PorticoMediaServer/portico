package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Mutate actual frame arguments AFTER a successful bind. Binder-only negatives
// cannot establish the connection/transaction boundary's authority check.
func TestGovernedTurnRejectsActualFrameMismatch(t *testing.T) {
	w, _, clock := testExecutor(t)
	for _, test := range []struct {
		name   string
		change func(*CommandID, *turnRecipe, *governedTurn)
	}{
		{"purpose_same_class", func(id *CommandID, _ *turnRecipe, _ *governedTurn) { *id = BasicAccept }},
		{"purpose_class_durability", func(id *CommandID, _ *turnRecipe, _ *governedTurn) { *id = SecurityAccept }},
		{"purpose_foreground", func(id *CommandID, _ *turnRecipe, _ *governedTurn) { *id = ProgressAccept }},
		{"purpose_background", func(id *CommandID, _ *turnRecipe, _ *governedTurn) { *id = EffectiveStage }},
		{"saved_class", func(_ *CommandID, _ *turnRecipe, c *governedTurn) { c.expected.Class = governor.Recovery }},
		{"rows_larger", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.Rows++ }},
		{"rows_smaller", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.Rows-- }},
		{"vm_ops", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.VMOps++ }},
		{"pages", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.Pages++ }},
		{"wal_bytes", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.WALBytes++ }},
		{"hold_larger", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.Hold += time.Millisecond }},
		{"hold_smaller", func(_ *CommandID, r *turnRecipe, _ *governedTurn) { r.limit.Hold -= time.Millisecond }},
	} {
		t.Run(test.name, func(t *testing.T) {
			now := func() time.Time { return *clock }
			g, a, wal, request := governedFixture(t, now)
			if err := wal.Publish(wal.Observation(), governor.WALState{PageSize: 4096}, now()); err != nil {
				t.Fatal(err)
			}
			grant := g.Try(request).Grant
			if grant == nil {
				t.Fatal("fixture admission failed")
			}
			custody, err := bindGovernedTurn(InventoryAccept, testRecipe().limit, g, a, wal, grant, request)
			if err != nil {
				t.Fatal(err)
			}
			id, recipe := InventoryAccept, testRecipe()
			test.change(&id, &recipe, custody)
			tx, err := w.begin(t.Context(), id, recipe, custody)
			if tx != nil {
				_, _ = tx.rollback(nil)
			}
			if tx != nil || !errors.Is(err, ErrInvalid) {
				t.Errorf("bound grant entered mismatched actual frame: tx=%v err=%v", tx != nil, err)
			}
			if a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) || w.db.Stats().InUse != 0 {
				t.Fatal("refusal stranded grant/connection")
			}
		})
	}
}
