package writer

import (
	"context"
	"errors"
	"testing"
)

func TestWCReclaimPreparedDrainOwnsSharedCommandSequence(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, pd, _ := wcReclaimCompositionOpening(t, principal)
	capture, err := pd.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	c := capture.(*wcProgressCommand)
	wantMS := c.request.Source.untilMS - c.request.Source.fromMS
	cap := pd.Disclosure()
	q, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(t.Context(), cap, capture); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckProgress(t.Context(), cap, q); err != nil {
		t.Fatal(err)
	}
	rd := wcReclaimForProgressFixture(t, w, pd)
	cancel()
	drain, err := pd.Drain(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	ref, _ := ProgressReference(drain)
	if ref.Receipt.Identity.Sequence != 2 {
		t.Fatal(ref)
	}
	// Reclaim must not steal sequence 2 between progress preparation and its
	// native COMMIT/ACK. In the regression this succeeded, and cleanup's ACK
	// permanently retired the still-unexecuted retained drain.
	if command, e := rd.Prepare(t.Context()); !errors.Is(e, ErrResolveRequired) || command != nil {
		t.Fatal("reclaim allocated the prepared drain's owner sequence", command, e)
	}
	if retry, e := pd.Drain(t.Context()); e != nil || retry != drain {
		t.Fatal("blocked cleanup displaced the original drain", retry, e)
	}
	result, err := pd.ExecuteDrain(t.Context(), drain)
	if err != nil || result.Outcome.State != Applied || result.Outcome.Replayed || result.Value.Finished.Int64 != 1 || result.Value.CreditedMs.Int64 != wantMS || result.Snapshot.Pending || result.Snapshot.PlayedMS != wantMS || result.RetainedAcknowledgment == nil || result.RetainedAcknowledgment.AcknowledgedThrough != 2 {
		t.Fatal("cleanup stranded retained time", result, wantMS, err)
	}
	wcAssertPublicProgressRevoked(t, w, pd, cap, ref)
	cleanup, err := rd.Prepare(t.Context())
	if err != nil {
		t.Fatal("actual retained ACK did not release command custody", err)
	}
	rr, _ := ReclaimReference(cleanup)
	if rr.Receipt.Identity.Sequence != 3 {
		t.Fatal("cleanup reused the retired drain sequence", rr)
	}
	if _, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), cleanup); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckReclaim(t.Context(), rd.Disclosure(), rr); err != nil {
		t.Fatal(err)
	}
}

func TestWCReclaimPreparedCleanupExcludesProgressUntilACK(t *testing.T) {
	w, pd, rd := wcReclaimCompositionProgressFixture(t)
	cleanup, err := rd.Prepare(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	q, _ := ReclaimReference(cleanup)
	assertExcluded := func() {
		t.Helper()
		if c, e := pd.Capture(t.Context(), 1, 6, 100, "playing"); !errors.Is(e, ErrResolveRequired) || c != nil {
			t.Fatal("capture overlapped cleanup custody", c, e)
		}
		if c, e := pd.Drain(t.Context()); !errors.Is(e, ErrResolveRequired) || c != nil {
			t.Fatal("drain overlapped cleanup custody", c, e)
		}
	}
	assertExcluded()
	if _, err = w.AcceptReclaim(t.Context(), rd.Disclosure(), cleanup); err != nil {
		t.Fatal(err)
	}
	assertExcluded() // COMMIT alone is not command retirement.
	if replay, e := w.ResolveReclaim(t.Context(), rd.Disclosure(), q); e != nil || !replay.Outcome.Replayed {
		t.Fatal(replay, e)
	}
	if _, err = w.AckReclaim(t.Context(), rd.Disclosure(), q); err != nil {
		t.Fatal(err)
	}
	capture, err := pd.Capture(t.Context(), 1, 6, 100, "playing")
	if err != nil {
		t.Fatal("actual cleanup ACK did not release owner custody", err)
	}
	ref, _ := ProgressReference(capture)
	if ref.Receipt.Identity.Sequence != q.Receipt.Identity.Sequence+1 {
		t.Fatal("capture did not use the next owner sequence", ref, q)
	}
	if _, err = w.AckReclaim(t.Context(), rd.Disclosure(), q); err != nil {
		t.Fatal(err)
	}
	if command, e := rd.Prepare(t.Context()); !errors.Is(e, ErrResolveRequired) || command != nil {
		t.Fatal("old cleanup ACK released a newer progress reservation", command, e)
	}
}
