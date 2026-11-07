package writer_test

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"os"
	"testing"
)

func TestWCBoundReclaimActionUsesOriginalNativeProtocol(t *testing.T) {
	if value := os.Getenv("PORTICO_CANONICAL_WC_FUNCTIONAL"); value == "" {
		t.Skip("real-clock WC Action control; set PORTICO_CANONICAL_WC_FUNCTIONAL=1")
	} else if value != "1" {
		t.Fatal("PORTICO_CANONICAL_WC_FUNCTIONAL must be 1 when requested")
	}
	w, d := writer.WCReclaimWorkloadFixture(t)
	binding := workload.ReclaimBinding{Writer: w, Driver: d}
	var raw []workload.Observation
	if err := workload.Reclaim(binding)(t.Context(), workload.Offer{}, func(o workload.Observation) error { raw = append(raw, o); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 5 {
		t.Fatal("missing real calls", len(raw))
	}
	for i, o := range raw {
		if o.Stub || o.Phase != []string{"SNAPSHOT", "ACCEPT", "RESOLVE", "ACK", "SNAPSHOT"}[i] || o.Evidence == nil || o.Evidence.Purpose != writer.ProgressReceiptReclaim || o.Evidence.Class != governor.Background || o.Evidence.Durability != writer.Normal || o.Evidence.Policy == nil || o.Evidence.Policy.Synchronous != 1 || o.Evidence.Commit != writer.CommitSucceeded || o.CallStarted == nil || o.CallReturned == nil || o.HoldNS == nil || len(o.EvidenceProblems) != 0 {
			t.Fatal("not actual BG/NORMAL", i, o)
		}
		if i == 3 && o.Outcome != nil {
			t.Fatal("ACK became Applied", o)
		}
	}
}
