package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

// CapabilityReport is what this host can do and, for anything it can't, the
// reason code and the concrete cause (Server › Logs & diagnostics).
type CapabilityReport struct {
	Items []capabilityreport.Status `json:"items"`
}

func registerCapabilityReport(r *apikit.Registry) {
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_capability_report", Method: "GET", Path: "/v1/admin/diagnostics/capabilities", Summary: "Why Live TV, recording, prepared media or hardware encoding is unavailable on this server",
		Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted"}},
		func(context.Context, *http.Request, noBody) (CapabilityReport, error) {
			items := capabilityreport.All()
			if items == nil {
				items = []capabilityreport.Status{}
			}
			return CapabilityReport{Items: items}, nil
		})
}
