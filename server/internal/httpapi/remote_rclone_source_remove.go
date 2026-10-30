package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
)

func registerRcloneSourceRemoval(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "remove_storage_source_rclone", Method: "DELETE", Path: "/v1/storage/sources/rclone/{id}", Summary: "Remove an unused imported source at its expected generation", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "administration_unavailable", "request_in_progress", "storage_command_conflict", "storage_operation_expired", "storage_command_capacity", "internal"}}, func(ctx context.Context, r *http.Request, in mounts.RcloneSourceRemoveInput) (RcloneSourceResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		if d.RemoteSources == nil || d.Mounts == nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return RcloneSourceResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
		receipt, err := d.Mounts.RemoveRcloneSource(ctx, string(actor), r.PathValue("id"), in, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		return RcloneSourceResponse{Scope: scope, Receipt: receipt}, nil
	})
}
