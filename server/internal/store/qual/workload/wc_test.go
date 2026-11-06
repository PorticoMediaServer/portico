package workload

import (
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
)

func TestWorkloadWCMixedCadenceIsBoundedAndGated(t *testing.T) {
	pattern := []WCPhase{ProgressCapturePhase, ProgressCapturePhase, ProgressDrainPhase, SecurityPhase, ProgressDrainPhase}
	stream, err := WCCadence("one-writer", time.Second, 0, pattern)
	if err != nil {
		t.Fatal(err)
	}
	pattern[0] = SecurityPhase // the frozen schedule must not borrow caller storage
	for i, step := range stream.Pattern {
		var observed []Observation
		err := step.Run(t.Context(), Offer{}, func(o Observation) error { observed = append(observed, o); return nil })
		if !errors.Is(err, ErrWCComposition) || len(observed) != 1 || !observed[0].Stub || observed[0].Evidence != nil || observed[0].HoldNS != nil || observed[0].CallStarted != nil {
			t.Fatal("native gate or null timing lost", i, err, observed)
		}
		intent := observed[0].Result.(WCIntent)
		if intent.Phase == SecurityPhase {
			if intent.Purpose != writer.SecurityAccept || intent.Class != governor.Recovery || intent.Durability != writer.Full || intent.Variant != "admin_revoke" {
				t.Fatal(intent)
			}
		} else if intent.Purpose != writer.ProgressAccept || intent.Class != governor.Foreground || intent.Durability != writer.Normal {
			t.Fatal(intent)
		}
		if i == 0 && intent.Phase != ProgressCapturePhase {
			t.Fatal("caller changed mix")
		}
	}
	for _, pattern := range [][]WCPhase{{SecurityPhase, SecurityPhase}, {ProgressCapturePhase, ProgressDrainPhase}, {ProgressCapturePhase, "unknown"}, make([]WCPhase, 33)} {
		if _, err := WCCadence("bad", time.Second, 0, pattern); err == nil {
			t.Fatal("invalid mix accepted")
		}
	}
}

func TestWorkloadWCEntryPointsNeverManufactureFULLCommit(t *testing.T) {
	for _, action := range []Action{ProgressCapture(), ProgressDrain(), Security()} {
		var o Observation
		outputErr := errors.New("raw sink failed")
		err := action(t.Context(), Offer{}, func(raw Observation) error { o = raw; return outputErr })
		if !errors.Is(err, ErrWCComposition) || !errors.Is(err, outputErr) || !o.Stub || o.Outcome != nil || o.CommitNS != nil || o.Evidence != nil {
			t.Fatal("intent or check became a physical sample", o, err)
		}
	}
}

func TestWorkloadProgressMetadataCannotTurnIntoPhysicalReport(t *testing.T) {
	o := syntheticObservation() // otherwise valid fabricated physical join control
	o.Progress = &ProgressFact{Linkage: &ProgressLinkStatus{Complete: true}}
	if _, err := RestartSample(testConfig().Cell, o, syntheticJoin(1)); !errors.Is(err, ErrWCComposition) {
		t.Fatal("metadata opened native projection", err)
	}
	raw := syntheticRun(o)
	coverage := Reduce(testConfig().Cell, raw, map[string]Classification{raw[1].UnitID: syntheticJoin(1)})
	if coverage.Report != nil || len(coverage.Problems) == 0 {
		t.Fatal("values-only lineage became whole-run report")
	}
}
