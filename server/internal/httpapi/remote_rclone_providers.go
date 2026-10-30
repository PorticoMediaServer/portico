package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
)

type RcloneConfigurationProvidersResponse struct {
	Scope     catalog.AdminScope `json:"scope"`
	Providers []string           `json:"providers"`
}

func registerRcloneConfigurationProviders(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_rclone_providers", Method: "GET", Path: "/v1/storage/sources/rclone/providers", Summary: "Read supported imported rclone configuration backend types", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "administration_unavailable", "internal"}}, func(_ context.Context, r *http.Request, _ struct{}) (RcloneConfigurationProvidersResponse, error) {
		_, scope, err := d.storageOwner(r)
		if err != nil {
			return RcloneConfigurationProvidersResponse{}, rcloneConnectionFailure(err)
		}
		if d.Mounts == nil || d.RemoteSources == nil {
			return RcloneConfigurationProvidersResponse{}, &apierror.Error{Code: "administration_unavailable"}
		}
		if r.URL.RawQuery != "" {
			return RcloneConfigurationProvidersResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		return RcloneConfigurationProvidersResponse{Scope: scope, Providers: mounts.SupportedConfigurationKinds()}, nil
	})
}
