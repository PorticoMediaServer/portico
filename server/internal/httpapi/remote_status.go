package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
)

type SourceStatusResponse struct {
	AssignmentFenceSupported bool                       `json:"assignmentFenceSupported"`
	Scope                    catalog.AdminScope         `json:"scope"`
	Status                   remotesources.SourceStatus `json:"status"`
}

// registerSourceStatus publishes one source's present state: its health in the
// server's words, the provider reads it holds now, when it was last checked and
// which libraries use its folders. It reads no provider.
func registerSourceStatus(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "administration_unavailable", "source_folders_unsupported", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_status", Method: "GET", Path: "/v1/storage/sources/{id}/status", Summary: "Read one remote source's present state and the libraries that use it", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (SourceStatusResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return SourceStatusResponse{}, folderError(err)
		}
		if d.RemoteSources == nil {
			return SourceStatusResponse{}, folderError(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return SourceStatusResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		status, err := d.RemoteSources.ReadSourceStatus(ctx, d.Identity, p, r.PathValue("id"))
		if err != nil {
			return SourceStatusResponse{}, folderError(err)
		}
		return SourceStatusResponse{Scope: scope, Status: status, AssignmentFenceSupported: true}, nil
	})
}
