package writer_test

import (
	"context"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"testing"
	"time"
)

func TestCanonicalHeldProgress(t *testing.T)  { runCanonicalWCClass(t, "progress") }
func TestCanonicalHeldSecurity(t *testing.T)  { runCanonicalWCClass(t, "security") }
func TestCanonicalHeldWCReclaim(t *testing.T) { runCanonicalWCClass(t, "wc_reclaim") }

// Exact merged Actions own source preparation + original/Resolve/ACK/current
// validation. This outer loop provides only fixed synthetic report data and IDs
// already born by trusted composition; it cannot mint command authority.
func runCanonicalWCClass(t *testing.T, class string) {
	t.Helper()
	c := writer.NewCanonicalWCComponent(t, class)
	runCanonicalWCComponent(t, c)
}
func TestCanonicalWCFunctional(t *testing.T) {
	for _, class := range []string{"progress", "security", "wc_reclaim"} {
		t.Run(class, func(t *testing.T) { runCanonicalWCComponent(t, writer.CanonicalWCFunctionalComponent(t, class)) })
	}
}
func runCanonicalWCComponent(t *testing.T, c *writer.CanonicalWCComponent) {
	t.Helper()
	class := c.Class
	var stop error
	defer func() {
		if err := c.Finish(stop); err != nil {
			t.Error(err)
		}
	}()
	ctx := t.Context()
	run := func(population, operation string, purpose writer.CommandID, action workload.Action) error {
		if err := c.Begin(population, operation, purpose); err != nil {
			return err
		}
		offer := workload.Offer{Stream: class, Operation: operation, ScheduledAt: time.Now()}
		err := action(ctx, offer, func(o workload.Observation) error {
			if value, ok := o.Result.(writer.ProgressSnapshot); ok && o.CallError == "" {
				c.ProgressSnapshot(value)
			}
			return c.Observe(writer.CanonicalWCObservation{Phase: o.Phase, Started: o.CallStarted, Returned: o.CallReturned, Evidence: o.Evidence, Requested: o.Requested, Outcome: o.Outcome, Error: o.CallError, Refused: o.RefusalError, Raw: o})
		})
		return c.End(err)
	}
	binding := workload.ProgressBinding{Writer: c.Writer, Driver: c.Progress, Report: func(context.Context, workload.Offer) (workload.ProgressReport, error) {
		report := workload.ProgressReport{Sequence: c.ReportSequence(), PositionSeconds: 5, DurationSeconds: 1000000, State: "playing"}
		return report, c.RecordProgressInput(report.Sequence, report.PositionSeconds, report.DurationSeconds, report.State)
	}}
	capture, drain := workload.ProgressCapture(binding), workload.ProgressDrain(binding)
	for i := 1; i <= c.Warmup+c.Attempts; i++ {
		if err := ctx.Err(); err != nil {
			stop = err
			return
		}
		population := "measured"
		if i <= c.Warmup {
			population = "warmup"
		}
		switch class {
		case "progress":
			if c.IsPending() {
				stop = run(population, "drain_piece", writer.ProgressAccept, drain)
			} else {
				stop = run(population, "capture_report", writer.ProgressAccept, capture)
			}
		case "security":
			if err := c.NextSecurityTarget(t, i); err != nil {
				stop = err
				return
			}
			action := workload.Security(workload.SecurityBinding{Writer: c.Writer, Driver: c.Security, Target: func(context.Context, workload.Offer) (string, error) { return c.Target(), nil }})
			stop = run(population, "admin_revoke", writer.SecurityAccept, action)
		case "wc_reclaim":
			// Retained debt comes only from actual fresh source calls. Each auxiliary
			// call is separately recorded and cannot inflate measured reclaim count.
			if stop = run("auxiliary", "capture_report", writer.ProgressAccept, capture); stop != nil {
				return
			}
			for piece := 0; c.IsPending() && piece < 2; piece++ {
				if stop = run("auxiliary", "drain_piece", writer.ProgressAccept, drain); stop != nil {
					return
				}
			}
			if c.IsPending() {
				stop = errors.New("source prefix exceeded two actual UTC-quarter pieces; incomplete, no repair")
				return
			}
			stop = run(population, "progress_receipt_reclaim", writer.ProgressReceiptReclaim, workload.Reclaim(workload.ReclaimBinding{Writer: c.Writer, Driver: c.Reclaim}))
		default:
			stop = errors.New("unknown compiled class")
		}
		if stop != nil {
			return
		}
	}
}
