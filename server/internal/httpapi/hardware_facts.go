package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/hardwarefacts"
)

// registerHardwareFacts publishes what the Streaming settings Hardware section
// cannot learn from the decoder alone: which install this server is, the one
// plain remedy for that install (HARDWARE HW-7), the V1 target matrix, the
// formats grid's order, and the capabilities V1 defers.
func registerHardwareFacts(r *apikit.Registry) {
	v1Route(r, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_hardware_facts", Method: "GET", Path: "/v1/admin/playback/hardware", Summary: "Read this server's hardware platform, its one remedy when no GPU is usable, and the V1 hardware target matrix",
		Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted"}},
		func(context.Context, *http.Request, noBody) (hardwarefacts.Document, error) {
			return hardwarefacts.Publish(), nil
		})
}
