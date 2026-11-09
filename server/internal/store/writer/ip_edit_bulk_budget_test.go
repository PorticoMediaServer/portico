package writer

import (
	"errors"
	"strings"
	"testing"
)

// ipBulkWideCommand builds a syntactically complete cohort of an arbitrary
// size. The entities need not exist: the budget refusal happens before any
// entity proof or write.
func ipBulkWideCommand(t *testing.T, identity CommandIdentity, count int) *ipEditBulkCommand {
	t.Helper()
	q := ipBulkRawRequest(identity)
	q.Targets = make([]ipEditBulkTarget, count)
	for j := range q.Targets {
		q.Targets[j] = ipEditBulkTarget{Ordinal: int64(j), EntityID: int64(1000 + j), Incarnation: [16]byte{13}, Revision: int64(j + 1)}
	}
	// The root must remain in its own immutable cohort.
	q.Targets[0] = ipEditBulkTarget{Ordinal: 0, EntityID: q.Root.RootEntity, Incarnation: [16]byte{4}, Revision: 3}
	q.Root.ExpectedCount = int64(count)
	q.Root.ExpectedBytes = ipBulkFieldBytes(q.Root.Fields) + ipEditBulkTargetBytes*int64(count)
	q.Root.Manifest = ipBulkManifestFor(q.Root, q.Targets)
	c, err := newIPEditBulkCommand(identity, q)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func ipBulkCount(t *testing.T, w *executor, query string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := w.db.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatal(query, err)
	}
	return n
}

// A cohort inside the governed statement/work budget still births and captures
// exactly as before.
func TestIPEditBulkBeginAtomicBudgetWithin(t *testing.T) {
	w, r, p := ipBulkFixture(t)
	cmd := ipBulkCommand(t, ipBulkIdentity(1, 2), nil)
	out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
	if err != nil || out.Outcome.State != Applied {
		t.Fatal(out, err)
	}
	ipTextCommit(t, turn, tx)
	id := cmd.request.Root.Operation
	for query, want := range map[string]int64{
		`SELECT count(*) FROM metadata_repair_cascades WHERE id=?`:                                  1,
		`SELECT descriptive_captured_count FROM metadata_repair_cascades WHERE id=?`:                2,
		`SELECT count(*) FROM metadata_edit_batch_fields WHERE operation_id=?`:                      1,
		`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id=?`:                     2,
		`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id=?`:                        1,
		`SELECT descriptive_input_complete FROM metadata_repair_cascades WHERE id=?`:                1,
		`SELECT count(*) FROM metadata_repair_cascades WHERE id=? AND descriptive_expected_count=2`: 1,
	} {
		if got := ipBulkCount(t, w, query, id); got != want {
			t.Fatal(query, got, want)
		}
	}
}

// One target over the computed atomic budget is refused before mutation with
// the typed marker and leaves no owner, cohort row or receipt.
func TestIPEditBulkBeginAtomicBudgetOverflowRefused(t *testing.T) {
	fields := 1
	maxTargets := 0
	for n := 1; ; n++ {
		count, ok := ipEditBulkBirthStatements(n, fields)
		if !ok || count > ipEditBulkAtomicStatementCeiling {
			break
		}
		maxTargets = n
	}
	if maxTargets < 2 {
		t.Fatal("atomic budget too small to exercise", maxTargets)
	}
	for name, count := range map[string]int{
		"one_target_over": maxTargets + 1,
		"logical_4096":    ipEditBulkMaxTargets,
	} {
		t.Run(name, func(t *testing.T) {
			w, r, p := ipBulkFixture(t)
			cmd := ipBulkWideCommand(t, ipBulkIdentity(1, 2), count)
			out, turn, tx, err := ipBulkApply(t, w, r, p, cmd)
			if !errors.Is(err, errIPEditBulkCohortExceedsAtomicBudget) || !errors.Is(err, ErrCap) {
				t.Fatal("expected typed pre-mutation refusal", err)
			}
			var refusal *PolicyError
			if !errors.As(err, &refusal) {
				t.Fatal("refusal is not a PolicyError", err)
			}
			for _, part := range []string{"targets=", "fields=", "statements=", "budget_statements=", "wal_bytes="} {
				if !strings.Contains(refusal.Field, part) {
					t.Fatal("PolicyError lost computed numbers", refusal.Field)
				}
			}
			if !ipEditBulkOutputZero(out) {
				t.Fatal("refusal returned an applied value", out)
			}
			ipTextRollback(t, turn, tx)
			id := cmd.request.Root.Operation
			for query, want := range map[string]int64{
				`SELECT count(*) FROM metadata_repair_cascades WHERE id=?`:              0,
				`SELECT count(*) FROM metadata_edit_batch_fields WHERE operation_id=?`:  0,
				`SELECT count(*) FROM metadata_edit_batch_targets WHERE operation_id=?`: 0,
				`SELECT count(*) FROM metadata_edit_batch_receipts WHERE owner_id=?`:    0,
			} {
				if got := ipBulkCount(t, w, query, id); got != want {
					t.Fatal(query, got, want)
				}
			}
		})
	}
}

// The logical 4096 bound is still the immutable cohort bound of the future
// paged design: preparation constructs it even though command 243 refuses to
// capture it atomically.
func TestIPEditBulkBeginLogicalCohortBoundPreparation(t *testing.T) {
	cmd := ipBulkWideCommand(t, ipBulkIdentity(1, 2), ipEditBulkMaxTargets)
	if !cmd.valid || cmd.request.Root.ExpectedCount != ipEditBulkMaxTargets || len(cmd.request.Targets) != ipEditBulkMaxTargets {
		t.Fatal("logical 4096 cohort failed preparation", cmd.valid, cmd.request.Root.ExpectedCount, len(cmd.request.Targets))
	}
}
