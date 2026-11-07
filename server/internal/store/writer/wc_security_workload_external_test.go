package writer_test

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/qual/workload"
	"github.com/PorticoMediaServer/portico/server/internal/store/writer"
	"os"
	"testing"
)

func TestWCBoundSecurityActionUsesFullOriginalProtocol(t *testing.T) {
	if value := os.Getenv("PORTICO_CANONICAL_WC_FUNCTIONAL"); value == "" {
		t.Skip("real-clock WC Action control; set PORTICO_CANONICAL_WC_FUNCTIONAL=1")
	} else if value != "1" {
		t.Fatal("PORTICO_CANONICAL_WC_FUNCTIONAL must be 1 when requested")
	}
	w, d, family := writer.WCSecurityWorkloadFixture(t)
	binding := workload.SecurityBinding{Writer: w, Driver: d, Target: func(context.Context, workload.Offer) (string, error) { return family, nil }}
	var raw []workload.Observation
	if err := workload.Security(binding)(t.Context(), workload.Offer{}, func(o workload.Observation) error { raw = append(raw, o); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(raw) != 5 {
		t.Fatal("missing real calls", len(raw))
	}
	for i, o := range raw {
		if o.Stub || o.Phase != []string{"SNAPSHOT", "ACCEPT", "RESOLVE", "ACK", "SNAPSHOT"}[i] || o.Evidence == nil || o.Evidence.Purpose != writer.SecurityAccept || o.Evidence.Class != governor.Recovery || o.Evidence.Durability != writer.Full || o.Evidence.Policy == nil || o.Evidence.Policy.Synchronous != 2 || o.Evidence.Commit != writer.CommitSucceeded || o.CallStarted == nil || o.CallReturned == nil || o.HoldNS == nil || len(o.EvidenceProblems) != 0 {
			t.Fatal("not actual FULL/Recovery", i, o)
		}
		if i == 3 && o.Outcome != nil {
			t.Fatal("ACK became Applied", o)
		}
	}
}
