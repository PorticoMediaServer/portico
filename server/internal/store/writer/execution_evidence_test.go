package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

func TestFrameEvidenceRollbackIsNotCommit(t *testing.T) {
	w, path, clock := testExecutor(t)
	now := func() time.Time { return *clock }
	g, a, wal, request := governedFixture(t, now)
	if err := refreshWriterWAL(path, wal, now); err != nil {
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
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	mutateFixture(t, tx)
	cause := errors.New("domain refused")
	result, err := tx.rollback(cause)
	if !errors.Is(err, cause) || result.state != rolledBack {
		t.Fatal(result, err)
	}
	e := frameEvidence(tx, custody)
	if e.Commit != CommitRolledBack || e.CommitAttemptedAt != nil || e.CommitReturnedAt != nil || e.PhysicalReturnedAt == nil {
		t.Fatal("rollback fabricated commit evidence", e)
	}
	if a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) || w.db.Stats().InUse != 0 {
		t.Fatal("rollback leaked physical custody")
	}
}

func TestFrameEvidenceRealGrantCommitAndReturn(t *testing.T) {
	w, path, clock := testExecutor(t)
	now := func() time.Time { return *clock }
	g, a, wal, request := governedFixture(t, now)
	if err := refreshWriterWAL(path, wal, now); err != nil {
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
	tx, err := w.begin(t.Context(), InventoryAccept, testRecipe(), custody)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.rollback(nil)
	if evidence := frameEvidence(tx, custody); evidence.Commit != 0 || evidence.PhysicalReturnedAt != nil {
		t.Fatal("unfinished turn fabricated terminal evidence", evidence)
	}
	mutateFixture(t, tx) // real SQLite effect, not a registered domain Applied
	*clock = clock.Add(time.Millisecond)
	result, err := tx.commit()
	if err != nil || result.state != committed {
		t.Fatal(result, err)
	}
	e := frameEvidence(tx, custody)
	if e.Commit != CommitSucceeded || e.Class != governor.ScanMinimum || e.Durability != Normal || e.Purpose != InventoryAccept ||
		e.Policy == nil || e.Policy.Engine != "3.53.4" || e.Policy.DQSDDL != 0 || e.Policy.DQSDML != 0 ||
		e.CommitAttemptedAt == nil || e.CommitReturnedAt == nil || e.PhysicalReturnedAt == nil ||
		e.CommitReturnedAt.Before(*e.CommitAttemptedAt) || e.PhysicalReturnedAt.Before(*e.CommitReturnedAt) ||
		e.PhysicalReturnedAt.Sub(e.HoldStartedAt) != result.hold || a.Usage()[governor.ScanMinimum] != (governor.Amounts{}) {
		t.Fatalf("incorrect physical evidence: %+v / %+v", e, result)
	}
	if !e.ReturnedAt.IsZero() || e.WALBefore != nil || e.WALAfter != nil {
		t.Fatal("frame invented dispatcher/WAL observations")
	}
	// A consumer cannot change the saved native-policy evidence inside the frame.
	e.Policy.Synchronous = 99
	if frameEvidence(tx, custody).Policy.Synchronous != int(Normal) {
		t.Fatal("evidence aliases mutable frame policy")
	}
}

func TestFrameEvidenceUnknownCommitAndFailedReturn(t *testing.T) {
	now := time.Now()
	c := &governedTurn{expected: governor.Request{Class: governor.Background}}
	tx := &transaction{start: now, descriptor: Descriptor{ID: EffectiveStage, Durability: Normal}, done: true, cleanupComplete: true,
		commitAttempted: true, commitStartedAt: now, result: turnResult{state: commitUnknown}}
	tx.settings.Engine = "partially observed, not asserted"
	e := frameEvidence(tx, c)
	if e.Commit != CommitUnknown || e.CommitAttemptedAt == nil || e.CommitReturnedAt != nil || e.PhysicalReturnedAt != nil || e.Policy != nil {
		t.Fatal("unknown/failed cleanup was fabricated as returned", e)
	}
	if frameEvidence(nil, c) != nil || frameEvidence(tx, nil) != nil {
		t.Fatal("missing holding fabricated evidence")
	}
}
