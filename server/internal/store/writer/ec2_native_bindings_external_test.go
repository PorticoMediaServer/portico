package writer_test

import (
	"context"
	"errors"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"testing"
	"time"
)

func boundEC2WC(c *writer.N1PairedComponent, purpose writer.CommandID, action workload.Action) workload.Action {
	return func(ctx context.Context, offer workload.Offer, emit func(workload.Observation) error) error {
		release, err := c.EnterEC2Writer(ctx)
		if err != nil {
			return err
		}
		defer release()
		before, beforeErr := c.ObserveEC2WAL(ctx)
		if beforeErr != nil {
			return beforeErr
		}
		err = action(ctx, offer, func(o workload.Observation) error {
			if snapshot, ok := o.Result.(writer.ProgressSnapshot); ok && o.CallError == "" {
				c.EC2ProgressSnapshot(snapshot)
			}
			observed := c.ObserveEC2WC(writer.CanonicalWCObservation{Phase: o.Phase, Started: o.CallStarted, Returned: o.CallReturned, Evidence: o.Evidence, Requested: o.Requested, Outcome: o.Outcome, Error: o.CallError, Refused: o.RefusalError, Raw: o}, purpose)
			return errors.Join(observed, emit(o))
		})
		after, afterErr := c.ObserveEC2WAL(context.WithoutCancel(ctx))
		return errors.Join(err, afterErr, emit(workload.Observation{Phase: "WC_WAL_BRACKET", Result: map[string]any{"before": before, "after": after, "after_error": afterErr}, Notice: "advisory full original-chain bracket, not per-turn/header-sync attribution"}))
	}
}

func TestEC2NativeFunctional(t *testing.T) {
	c := writer.EC2NativeFunctionalComponent(t)
	bindings := ec2Bindings(t, c, "timing")
	offer := workload.Offer{Stream: "functional", Slot: 1, ScheduledAt: time.Now()}
	consume := func(o workload.Observation) error {
		if o.Stub || o.CallError != "" || o.RefusalError {
			return fmt.Errorf("partial real native observation: %+v", o)
		}
		return nil
	}
	for _, op := range []workload.EC2Operation{workload.EC2Inventory, workload.EC2ProgressCapture} {
		if err := bindings[op](t.Context(), offer, consume); err != nil {
			t.Fatal(op, err)
		}
	}
	for i := 0; c.EC2ProgressPending() && i < 2; i++ {
		if err := bindings[workload.EC2ProgressDrain](t.Context(), offer, consume); err != nil {
			t.Fatal(err)
		}
	}
	if c.EC2ProgressPending() {
		t.Fatal("pending source remains after bounded real drain")
	}
	for _, op := range []workload.EC2Operation{workload.EC2Security, workload.EC2Reclaim, workload.EC2Passive, workload.EC2Recovery} {
		if err := bindings[op](t.Context(), offer, consume); err != nil {
			t.Fatal(op, err)
		}
	}
	if err := c.Close(nil); err != nil {
		t.Fatal(err)
	}
}
