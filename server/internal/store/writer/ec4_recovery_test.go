package writer

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

// An expired producer preparation cannot lend its old lifetime to history.
// Close joins that exact owner before W funds recovery of the SAME original.
func ec4ResolveOriginal(ctx context.Context, d *ScanDriver, p *N1Preparation, q ScanReceiptRequest) (InventoryResult, error) {
	if err := p.Close(ctx); err != nil {
		return InventoryResult{}, err
	}
	return d.ResolveScan(ctx, q)
}

func TestEC4OriginalRecovery(t *testing.T) {
	for _, applied := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending_stays_pending", true: "discarded_applied"}[applied], func(t *testing.T) {
			d, p, command, original := n1ComposedDriverPrepared(t, n1OperatingForecastFixture)
			if applied {
				base, cancel := context.WithCancel(t.Context())
				defer cancel()
				ctx := &scanIssuerPublicationContext{Context: base, cancel: cancel, installed: func() bool { return p.resultPublished }}
				got, err := p.Accept(ctx, command)
				if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, InventoryResult{}) {
					t.Fatal("discarded publication must have zero output", got, err)
				}
			}
			got, err := ec4ResolveOriginal(t.Context(), d, p, original)
			if err != nil || got.Outcome.Reference != original.Receipt {
				t.Fatal("same-original resolution", got, err)
			}
			if applied {
				if got.Outcome.State != Applied || !got.Outcome.Replayed || len(got.Members) != 1 {
					t.Fatal("lost saved Applied", got)
				}
				ack, err := d.AckScan(t.Context(), original)
				if err != nil || ack.AcknowledgedThrough != original.Receipt.Identity.Sequence {
					t.Fatal("same-original ACK", ack, err)
				}
			} else {
				if got.Outcome.State != Pending || len(got.Members) != 0 {
					t.Fatal("invented application", got)
				}
				if _, err := d.AckScan(t.Context(), original); !errors.Is(err, ErrResolveRequired) {
					t.Fatal("pending original advanced ACK", err)
				}
			}
			// Existing public history consumer Close, never an ACK/refund shortcut.
			if err := d.ReleasePending(t.Context()); err != nil || d.preparation != nil {
				t.Fatal("history custody did not release", err)
			}
		})
	}
}
