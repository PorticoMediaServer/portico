package writer

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Real W turns and virtual elapsed time with maintenance observation enabled;
// no wall-clock latency assertion.
func TestN1SustainedPublishesEachPhysicalTerminal(t *testing.T) {
	f := progressiveProtocolFixture(t, 1)
	f.owner.maintenance = true
	command, err := f.NextOriginal()
	if err != nil {
		t.Fatal(err)
	}
	commitProgressiveOriginal(t, f)
	check := func(tx *transaction) {
		t.Helper()
		view := f.owner.postReturn
		if f.owner.observedReturn != tx || view == nil || view.ObservedAt.Before(tx.physicalReturnedAt) || view.ObservedAt.Before(tx.commitReturnedAt) {
			t.Fatal("missing actual post-return publication")
		}
		if _, e := f.owner.wal.RestartRisk(f.owner.executor.now()); e != nil {
			t.Fatal("gate still awaiting writer sample", e)
		}
		if e := f.owner.observeReturned(tx); e != nil || f.owner.postReturn != view {
			t.Fatal("physical terminal sampled twice", e)
		}
	}
	check(f.turn.transaction)
	if err := f.Resolve(t); err != nil {
		t.Fatal(err)
	}
	check(f.resolveTurn.transaction)
	if err := f.AckAndSnapshot(t); err != nil {
		t.Fatal(err)
	}
	check(f.ackTurn.transaction)
	if !f.ready || f.acknowledged == nil || *f.acknowledged != command.identity.Sequence {
		t.Fatal("lost actual progression")
	}
}

func TestN1SustainedEarlyGuardCannotBorrowAdmission(t *testing.T) {
	f := progressiveProtocolFixture(t, 1)
	if _, err := f.NextOriginal(); err != nil {
		t.Fatal(err)
	}
	// Existing test decorator seam: retain a real physical observer for the
	// failed call's truthful wire; it never enters a writer transaction.
	w := f.owner.executor
	if err := w.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.owner.path, w.config)
	if err != nil {
		t.Fatal(err)
	}
	trace := newN1Trace(time.Now(), false)
	w.openPool(connector, trace.decorate)
	if _, err := f.currentSnapshot(t); err != nil {
		t.Fatal(err)
	}
	f.owner.lastAdmission = &n1Admission{Request: f.owner.request}
	stream := n1ControlStream(t)
	summary := &n1SteadySummary{Turns: map[string]*n1TurnCensus{"resolve": {}}}
	record := runN1SteadyTurn(t, t.Context(), stream, f, trace, summary, 1, 1, "measured", "resolve", "timing", trace.conn.id, n1SteadyControls{})
	if f.owner.lastAdmission != nil || record.Admission != nil || record.Clock.HoldNS != nil || record.Complete || !errors.Is(f.stopped, ErrResolveRequired) {
		t.Fatal("early Resolve guard borrowed previous admission", record.Admission, record.Error)
	}
}
