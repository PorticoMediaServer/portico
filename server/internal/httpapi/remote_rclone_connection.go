package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type RcloneConnectionResponse struct {
	Scope  catalog.AdminScope            `json:"scope"`
	Result mounts.RcloneConnectionResult `json:"result"`
}

func rcloneConnectionFailure(err error) error {
	switch {
	case errors.Is(err, mounts.ErrConfigInvalid), errors.Is(err, mounts.ErrInvalid):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, mounts.ErrExecutableInvalid), errors.Is(err, mounts.ErrCommandCapacity):
		return &apierror.Error{Code: "administration_unavailable"}
	case errors.Is(err, mounts.ErrExecutableTooOld):
		// These routes resolve the bundled program, so too-old is not an
		// emittable chooser fact here; keep the declared administration code
		// rather than falling through to internal.
		return &apierror.Error{Code: "administration_unavailable"}
	case errors.Is(err, storage.ErrBusy):
		// The same existing busy retry a saved source's Test uses when its
		// admitted operations do not drain (§40).
		return &apierror.Error{Code: "request_in_progress"}
	default:
		// This projection deliberately discards transport/credential causes.
		return davConnectionFailure(err)
	}
}

func registerRcloneConnectionTest(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "test_storage_source_rclone", Method: "POST", Path: "/v1/storage/sources/rclone/test", Summary: "Test an unsaved imported rclone source configuration", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "source_credentials_required", "administration_unavailable", "request_in_progress", "internal"}}, func(ctx context.Context, r *http.Request, in mounts.RcloneConnectionInput) (RcloneConnectionResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return RcloneConnectionResponse{}, rcloneConnectionFailure(err)
		}
		if d.Mounts == nil || d.RemoteSources == nil {
			return RcloneConnectionResponse{}, rcloneConnectionFailure(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return RcloneConnectionResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		result, err := d.Mounts.TestRcloneConnection(ctx, in, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return RcloneConnectionResponse{}, rcloneConnectionFailure(err)
		}
		if _, _, err := d.storageOwner(r); err != nil {
			return RcloneConnectionResponse{}, rcloneConnectionFailure(err)
		}
		return RcloneConnectionResponse{Scope: scope, Result: result}, nil
	})
}
