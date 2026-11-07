package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func wcAssertPublicProgressRevoked(t *testing.T, w *Writer, d *WCProgressDriver, cap *ProgressDisclosure, q ProgressReceiptRequest) {
	t.Helper()
	if out, e := w.ResolveProgress(t.Context(), cap, q); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, ProgressResult{}) {
		t.Fatal("retained owner disclosed public result", out, e)
	}
	if out, e := w.AckProgress(t.Context(), cap, q); !errors.Is(e, ErrDisclosureDenied) || !reflect.DeepEqual(out, AckResult{}) {
		t.Fatal("retained owner granted public ACK", out, e)
	}
	if _, e := d.Capture(t.Context(), 1, 6, 100, "playing"); e == nil {
		t.Fatal("retained owner minted capture")
	}
}

func TestWCProgressRetainedCaptureCommitBeforePublicACK(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, d, _ := wcProgressCompositionFixture(t, principal)
	cap := d.Disclosure()
	capture, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	c := capture.(*wcProgressCommand)
	expected := c.request.Source.untilMS - c.request.Source.fromMS
	q, _ := ProgressReference(capture)
	if out, e := w.AcceptProgress(t.Context(), cap, capture); e != nil || out.Outcome.State != Applied {
		t.Fatal(out, e)
	}
	cancel() // native capture is committed, but its public ACK has NOT run
	wcAssertPublicProgressRevoked(t, w, d, cap, q)
	drain, err := d.Drain(t.Context())
	if err != nil {
		t.Fatal("retained capture could not retire its own original", err)
	}
	result, err := d.ExecuteDrain(t.Context(), drain)
	if err != nil || result.Outcome.State != Applied || result.Value.Finished.Int64 != 1 || result.Value.CreditedMs.Int64 != expected || result.RetainedAcknowledgment == nil || result.RetainedAcknowledgment.AcknowledgedThrough != 2 || result.Snapshot.Last != 2 || result.Snapshot.Acknowledged != 2 || result.Snapshot.Pending || result.Snapshot.PlayedMS != expected {
		t.Fatal("pending obligation or floor stranded", result, err)
	}
	if d.work != nil || d.pending.Load() != nil {
		t.Fatal("completed retained owner remained unresolved")
	}
	wcAssertPublicProgressRevoked(t, w, d, cap, q)
}

func TestWCProgressRetainedCrossQuarterRetiresEveryPiece(t *testing.T) {
	principal, cancel := context.WithCancel(t.Context())
	defer cancel()
	w, d, _ := wcProgressCompositionFixture(t, principal)
	cap := d.Disclosure()
	// Historical source endpoints are fixture DATA, spanning a past quarter.
	// Every executor/admission/applied/terminal clock remains real time.Now.
	// No sleeping to a wall-clock boundary or source-duration cap is needed.
	now := time.Now().UnixMilli()
	d.intervalFrom = (now/wcQuarterMS)*wcQuarterMS - wcQuarterMS - 1
	capture, err := d.Capture(t.Context(), 0, 5, 100, "playing")
	if err != nil {
		t.Fatal(err)
	}
	c := capture.(*wcProgressCommand)
	expected := c.request.Source.untilMS - c.request.Source.fromMS
	q, _ := ProgressReference(capture)
	if _, err = w.AcceptProgress(t.Context(), cap, capture); err != nil {
		t.Fatal(err)
	}
	if _, err = w.AckProgress(t.Context(), cap, q); err != nil {
		t.Fatal(err)
	}
	cancel()
	var credited int64
	var last ProgressResult
	finished := false
	for piece := int64(0); piece < 3; piece++ {
		command, e := d.Drain(t.Context())
		if e != nil {
			t.Fatal("next retained piece blocked", piece, e)
		}
		ref, _ := ProgressReference(command)
		last, e = d.ExecuteDrain(t.Context(), command)
		if e != nil || last.Outcome.State != Applied || last.Outcome.Replayed || last.RetainedAcknowledgment == nil || last.RetainedAcknowledgment.AcknowledgedThrough != ref.Receipt.Identity.Sequence || last.Snapshot.Last != ref.Receipt.Identity.Sequence || last.Snapshot.Acknowledged != last.Snapshot.Last || last.Value.SourceReportSequence != 0 {
			t.Fatal("retained piece reused old result/floor", piece, last, e)
		}
		if piece == 0 && last.Value.Finished.Int64 != 0 {
			t.Fatal("cross-quarter source was not split")
		}
		credited += last.Value.CreditedMs.Int64
		wcAssertPublicProgressRevoked(t, w, d, cap, ref)
		if last.Value.Finished.Int64 == 1 {
			finished = true
			break
		}
	}
	if !finished || credited != expected || last.Snapshot.Pending || last.Snapshot.PlayedMS != expected || d.work != nil || d.pending.Load() != nil {
		t.Fatal("retained remainder or final floor stranded", credited, expected, last)
	}
}
