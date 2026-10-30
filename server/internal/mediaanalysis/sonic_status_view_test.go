package mediaanalysis

import (
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/sonic"
	"github.com/PorticoMediaServer/portico/server/internal/workpolicy"
)

func TestSonicOwnerViewUsesPolicyAndCodesWithoutFabricatedProgress(t *testing.T) {
	at := time.Date(2026, 10, 24, 1, 0, 0, 0, time.UTC)
	next := at.Add(time.Hour)
	eta := int64(120)
	status := SonicStatus{Settings: catalog.SonicSettings{Enabled: true, Revision: 3}, PolicyAllows: true, CanAnalyze: true, Total: 10, Analyzed: 4,
		Runtime: sonic.Availability{State: "ready"}, Work: workpolicy.HeavyWorkFacts{Revision: 7, ObservedAt: at, WindowOpen: true}, EstimatedRemainingSeconds: &eta}
	view := status.OwnerView("music")
	if view.State != "analyzing" || view.Done != 4 || view.Total != 10 || view.EstimatedRemainingSeconds == nil || view.ObservedAtMS != at.UnixMilli() {
		t.Fatal(view)
	}
	status.Work.WindowOpen = false
	status.Work.NextAllowedAt = &next
	view = status.OwnerView("music")
	if view.State != "paused" || view.PausedUntilMS == nil || *view.PausedUntilMS != next.UnixMilli() || view.EstimatedRemainingSeconds != nil {
		t.Fatal(view)
	}
	status.Analyzed = 10
	if view = status.OwnerView("music"); view.State != "complete" || view.PausedUntilMS != nil {
		t.Fatal(view)
	}
	status.CanAnalyze = false
	status.Runtime.Reason = "private loader diagnostic with /path"
	if view = status.OwnerView("music"); view.State != "unavailable" || view.ReasonCode != "runtime_or_model_missing" || view.Done != 10 {
		t.Fatal(view)
	}
	status.Settings.Enabled = false
	if view = status.OwnerView("music"); view.State != "disabled" || view.ReasonCode != "" || view.Done != 10 {
		t.Fatal(view)
	}
}

func TestSonicOwnerAvailabilityIsVerifiedAndIndependentOfLibraryChoice(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, policyAllows := range []bool{false, true} {
			for _, runtime := range []sonic.Availability{
				{State: "ready"},
				{State: "unavailable", Reason: "inference_not_verified", Model: &sonic.Identity{ID: sonic.ModelID}},
				{State: "unavailable", Reason: "inference_failed", Model: &sonic.Identity{ID: sonic.ModelID}},
				{State: "unavailable", Reason: "runtime_or_model_missing"},
			} {
				status := SonicStatus{Settings: catalog.SonicSettings{Enabled: enabled, Revision: 1}, PolicyAllows: policyAllows,
					CanAnalyze: true, Runtime: runtime, Total: 10, Analyzed: 10,
					Work: workpolicy.HeavyWorkFacts{ObservedAt: time.Now(), WindowOpen: true}}
				view := status.OwnerView("music")
				if view.DefaultEnabled != catalog.SonicDefaultEnabled() || view.Available != (runtime.State == "ready") || view.Enabled != enabled || view.PolicyAllows != policyAllows {
					t.Fatalf("enabled=%v policy=%v runtime=%+v view=%+v", enabled, policyAllows, runtime, view)
				}
			}
		}
	}
}
