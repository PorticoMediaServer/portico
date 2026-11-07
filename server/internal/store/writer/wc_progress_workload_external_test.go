package writer_test

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"testing"
)

func TestWCBoundProgressActionsUseOriginalNativeProtocol(t *testing.T) {
	w, d := writer.WCProgressWorkloadFixture(t)
	binding := workload.ProgressBinding{Writer: w, Driver: d, Report: func(context.Context, workload.Offer) (workload.ProgressReport, error) {
		return workload.ProgressReport{Sequence: 0, PositionSeconds: 5, DurationSeconds: 100, State: "playing"}, nil
	}}
	raw := []workload.Observation{}
	emit := func(o workload.Observation) error { raw = append(raw, o); return nil }
	if err := workload.ProgressCapture(binding)(t.Context(), workload.Offer{}, emit); err != nil {
		t.Fatal(err)
	}
	if err := workload.ProgressDrain(binding)(t.Context(), workload.Offer{}, emit); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 10 {
		t.Fatal("missing actual writer calls", len(raw))
	}
	for i, o := range raw {
		expected := []string{"SNAPSHOT", "ACCEPT", "RESOLVE", "ACK", "SNAPSHOT"}[i%5]
		if o.Stub || o.Phase != expected || o.Evidence == nil || o.Evidence.Purpose != writer.ProgressAccept || o.Evidence.Class != governor.Foreground || o.Evidence.Durability != writer.Normal || o.Evidence.Policy == nil || o.Evidence.Policy.Synchronous != 1 || o.Evidence.Commit != writer.CommitSucceeded || o.CallStarted == nil || o.CallReturned == nil || o.HoldNS == nil || len(o.EvidenceProblems) != 0 {
			t.Fatal("intent replaced native protocol evidence", i, o)
		}
		if i%5 == 3 && o.Outcome != nil {
			t.Fatal("ACK became Applied", o)
		}
	}
	after, ok := raw[9].Result.(writer.ProgressSnapshot)
	if !ok || after.Pending || after.Last != 2 || after.Acknowledged != 2 || after.ReceiptRows != 2 || after.PlayedMS <= 0 || !after.LastEvent.Valid || after.LastEvent.Int64 != 6 {
		t.Fatal("missing native current-state/debt/event confirmation", raw[9])
	}
}
