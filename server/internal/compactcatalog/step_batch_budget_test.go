package compactcatalog

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPacedStepFamiliesUseTheirExistingFourUnitBudget(t *testing.T) {
	db := cc1OpenDB(t, "step-budget")
	original := steps
	t.Cleanup(func() { steps = original })
	for i := range steps {
		steps[i] = func(context.Context, *sql.Tx, int) (int, error) { return 0, nil }
	}
	for _, family := range []int{0, 1, 2, 3, 6, 10, bookEvidenceStep, bookVisibleStep, recommendationTasteStep, readingAttributionStep, readingAttributionStep + 1} {
		t.Run(fmt.Sprintf("family-%d", family), func(t *testing.T) {
			w := NewWorker(db)
			w.PaceToHold(BatchHoldTarget)
			w.stepTurn = family
			calls := 0
			steps[family] = func(ctx context.Context, tx *sql.Tx, limit int) (int, error) {
				calls++
				if limit < 1 || limit > 4 {
					t.Fatalf("raw allowance widened: %d", limit)
				}
				_, err := tx.ExecContext(ctx, `UPDATE catalog_visibility_sort_state SET turn=turn WHERE id=1`)
				return 1, err
			}
			defer func() { steps[family] = func(context.Context, *sql.Tx, int) (int, error) { return 0, nil } }()
			dbwork.ResetPlans()
			var total, kind int
			ctx, cost := dbwork.Measure(context.Background())
			if err := dbwork.WithWriteTx(ctx, db, dbwork.ClassMaintenance, func(tx *sql.Tx) error {
				var err error
				total, kind, err = w.runSteps(ctx, tx, 64)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			want := 4
			if family == bookEvidenceStep || family == bookVisibleStep || family == recommendationTasteStep || family == readingAttributionStep || family == readingAttributionStep+1 {
				want = 1
			}
			if total != want || calls != want || kind != family {
				t.Fatalf("family %d: units=%d calls=%d kind=%d want=%d", family, total, calls, kind, want)
			}
			if cost().Transactions != 1 || dbwork.Plans().Overruns != 0 {
				t.Fatalf("step escaped one writer or its plan: cost=%+v plans=%+v", cost(), dbwork.Plans())
			}
			if w.stepTurn != (family+1)%len(steps) {
				t.Fatal("batch did not rotate to its sibling family")
			}
		})
	}
}
