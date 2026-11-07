package writer

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"testing"
	"time"
)

func wcReclaimCompositionOpening(t *testing.T, principalLifetimes ...context.Context) (*Writer, *WCProgressDriver, *governor.Authority) {
	return wcReclaimCompositionOpeningClock(t, false, principalLifetimes...)
}
func wcReclaimCompositionOpeningClock(t *testing.T, realClock bool, principalLifetimes ...context.Context) (*Writer, *WCProgressDriver, *governor.Authority) {
	t.Helper()
	opts := wcFixtureOptions{kind: "wc_reclaim", actorTier: "member", extraMemory: 1 << 20, maxGrants: 2, realClock: realClock}
	if len(principalLifetimes) == 1 {
		opts.principal = principalLifetimes[0]
	}
	w, d, a, _ := wcComponentFixture(t, opts)
	return w, d, a
}
func wcReclaimCompositionFixture(t *testing.T, principals ...context.Context) (*Writer, *WCReclaimDriver) {
	w, _, d := wcReclaimCompositionProgressFixture(t, principals...)
	return w, d
}

func wcReclaimCompositionProgressFixture(t *testing.T, principals ...context.Context) (*Writer, *WCProgressDriver, *WCReclaimDriver) {
	return wcReclaimCompositionProgressFixtureClock(t, false, principals...)
}

func wcReclaimCompositionProgressFixtureClock(t *testing.T, realClock bool, principals ...context.Context) (*Writer, *WCProgressDriver, *WCReclaimDriver) {
	t.Helper()
	w, pd, _ := wcReclaimCompositionOpeningClock(t, realClock, principals...)
	ctx := t.Context()
	capture, err := pd.Capture(ctx, 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	q, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(ctx, pd.Disclosure(), capture); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckProgress(ctx, pd.Disclosure(), q); err != nil {
		t.Fatal(err)
	}
	finished := false
	for i := 0; i < 2; i++ {
		c, e := pd.Drain(ctx)
		if e != nil {
			t.Fatal(e)
		}
		result, e := pd.ExecuteDrain(ctx, c)
		if e != nil {
			t.Fatal(e)
		}
		ref, _ := ProgressReference(c)
		if _, e = w.AckProgress(ctx, pd.Disclosure(), ref); e != nil {
			t.Fatal(e)
		}
		if result.Value.Finished.Int64 == 1 {
			finished = true
			break
		}
	}
	if !finished {
		t.Fatal("short native source did not finish within two actual quarters")
	}
	return w, pd, wcReclaimForProgressFixture(t, w, pd)
}

func wcReclaimForProgressFixture(t *testing.T, w *Writer, pd *WCProgressDriver) *WCReclaimDriver {
	t.Helper()
	ctx := t.Context()
	var err error
	r := w.effective
	p := *r.wcProgressPlan
	p.steps = nil
	p.wcProgressMemory = commandMemoryBudget{}
	p.wcReclaimMemory = commandMemoryBudget{64 << 20, 64 << 20}
	for _, d := range wcReclaimCommandDefinitions() {
		p.steps = append(p.steps, statementStep{d.ID, Work{16, 4000, 16, 65536, 100 * time.Microsecond}})
	}
	if err = composeWCReclaim(ctx, r, p); err != nil {
		t.Fatal(err)
	}
	expected := pd.expected
	expected.Key.Domain = uint16(ProgressReceiptReclaim)
	expected.Class = governor.Background
	expected.Footprint = []governor.Scope{{Library: pd.scope.libraryID, Domain: uint16(ProgressReceiptReclaim)}}
	decision := r.governor.Try(expected)
	if decision.Grant == nil {
		t.Fatal(decision.Refusal)
	}
	d, err := newWCReclaimDriver(ctx, w, pd, expected, decision.Grant)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := d.Close(context.Background()); e != nil {
			t.Error(e)
		}
	})
	return d
}
func TestWCReclaimCompositionOriginalReplayAckAndActualChildAbsence(t *testing.T) {
	w, d := wcReclaimCompositionFixture(t)
	before, err := w.CurrentReclaim(t.Context(), d.Disclosure())
	if err != nil {
		t.Fatal(err)
	}
	if before.ReceiptRows < 2 || !before.Due.Valid || before.Last != before.Acknowledged {
		t.Fatal(before)
	}
	for i := int64(0); i < before.ReceiptRows; i++ {
		prior, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil {
			t.Fatal(err)
		}
		c, err := d.Prepare(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		q, _ := ReclaimReference(c)
		result, err := w.AcceptReclaim(t.Context(), d.Disclosure(), c)
		if err != nil || result.Outcome.State != Applied || result.Outcome.Replayed || result.Value.RemovedCount != 1 || result.Value.RemovedSequence.Int64 != i+1 || q.Receipt.Identity.Sequence != prior.Last+1 {
			t.Fatal(result, err)
		}
		e := result.Outcome.Evidence
		if e == nil || e.Purpose != ProgressReceiptReclaim || e.Class != governor.Background || e.Durability != Normal || e.Policy == nil || e.Policy.Synchronous != 1 || e.Commit != CommitSucceeded {
			t.Fatal("not actual BG/NORMAL", e)
		}
		native, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil || native.LastRemovedChildPresent || native.ReceiptRows != prior.ReceiptRows-1 || prior.ReceiptBytes-native.ReceiptBytes != result.Value.RemovedVariableBytes || native.AfterSequence != i+1 || native.LastEvent != before.LastEvent {
			t.Fatal("physical child deletion/debit missing", native, err)
		}
		saved, err := w.ResolveReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || !saved.Outcome.Replayed || saved.Value != result.Value || saved.Digest != result.Digest {
			t.Fatal(saved, err)
		}
		after, err := w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil {
			t.Fatal(err)
		}
		native.Evidence, after.Evidence = nil, nil
		if native != after {
			t.Fatal("replay changed effects", native, after)
		}
		ack, err := w.AckReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || ack.AcknowledgedThrough != q.Receipt.Identity.Sequence {
			t.Fatal(ack, err)
		}
		after, err = w.CurrentReclaim(t.Context(), d.Disclosure())
		if err != nil || after.ReceiptRows != native.ReceiptRows || after.ReceiptBytes != native.ReceiptBytes || after.ControlBytes != native.ControlBytes || after.Due != native.Due || after.Slot != native.Slot || after.LastEvent != native.LastEvent {
			t.Fatal("cleanup ACK cleared slot/refunded/redue", after, err)
		}
		retired, err := w.ResolveReclaim(t.Context(), d.Disclosure(), q)
		if err != nil || retired.Outcome.State != ResultRetired || retired.Value != (ReclaimValue{}) {
			t.Fatal(retired, err)
		}
	}
	final, err := w.CurrentReclaim(t.Context(), d.Disclosure())
	if err != nil || final.ReceiptRows != 0 || final.ReceiptBytes != 0 || final.Due.Valid || !final.SlotPresent || !final.Slot.ResultNoNext || final.LastRemovedChildPresent {
		t.Fatal(final, err)
	}
	if _, err = d.Prepare(t.Context()); !errors.Is(err, ErrConflict) {
		t.Fatal("idle loop minted cleanup work", err)
	}
}
func TestWCReclaimCompositionRetainedOwnerOutlivesCurrentUser(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, d := wcReclaimCompositionFixture(t, principal)
	cancel()
	c, err := d.Prepare(t.Context())
	if err != nil {
		t.Fatal("retained claim lost cleanup", err)
	}
	if out, e := w.AcceptReclaim(t.Context(), d.Disclosure(), c); e != nil || out.Outcome.State != Applied {
		t.Fatal(out, e)
	}
}
