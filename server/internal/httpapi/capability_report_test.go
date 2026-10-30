package httpapi

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

// The owner sees why a capability is off, with the concrete cause; members
// don't see diagnostics at all.
func TestCapabilityReportShowsTheCause(t *testing.T) {
	f := newV1Fixture(t, 1)
	capabilityreport.Report(capabilityreport.Recording, false, "decoder_dependencies_unavailable", errors.New("library resolution exceeded 256 files"))
	var report CapabilityReport
	f.call("GET", "/v1/admin/diagnostics/capabilities", nil, nil, 200, &report)
	found := false
	for _, s := range report.Items {
		found = found || s.Capability == "recording" && !s.Available && s.Code == "decoder_dependencies_unavailable" && s.Detail == "library resolution exceeded 256 files"
	}
	if !found {
		t.Fatalf("report %+v", report)
	}
	member, _ := f.member()
	if w := f.raw("GET", "/v1/admin/diagnostics/capabilities", member, nil, nil); w.Code != 401 && w.Code != 403 {
		t.Fatalf("member read diagnostics: %d", w.Code)
	}
	capabilityreport.Report(capabilityreport.Recording, true, "", nil)
}
