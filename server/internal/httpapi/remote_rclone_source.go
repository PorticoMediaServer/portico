package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type RcloneSourceResponse struct {
	Scope   catalog.AdminScope         `json:"scope"`
	Receipt mounts.RcloneSourceReceipt `json:"receipt"`
}

func rcloneSourceErrorDefinitions() []apierror.Definition {
	return []apierror.Definition{
		{Code: "storage_executable_invalid", Status: 400, Message: publicErrorMessage("storage_executable_invalid"), Retry: apierror.Never},
		{Code: "storage_executable_too_old", Status: 400, Message: publicErrorMessage("storage_executable_too_old"), Retry: apierror.Never},
		{Code: "storage_command_conflict", Status: 409, Message: publicErrorMessage("storage_command_conflict"), Retry: apierror.AfterRefresh},
		{Code: "storage_operation_expired", Status: 410, Message: publicErrorMessage("storage_operation_expired"), Retry: apierror.Never},
		{Code: "storage_command_capacity", Status: 429, Message: publicErrorMessage("storage_command_capacity"), Retry: apierror.SameRequest},
		{Code: "storage_allocation_recovery_required", Status: 409, Message: publicErrorMessage("storage_allocation_recovery_required"), Retry: apierror.AfterRefresh},
	}
}

func rcloneSourceFailure(err error) error {
	code := ""
	switch {
	case errors.Is(err, mounts.ErrSourceInUse), errors.Is(err, storage.ErrBusy):
		// A live source's Test returns Busy when its admitted operations do not
		// drain in five seconds (§40); present it as the existing busy retry.
		code = "request_in_progress"
	case errors.Is(err, sql.ErrNoRows):
		code = "not_found"
	case errors.Is(err, mounts.ErrCommandConflict):
		code = "storage_command_conflict"
	case errors.Is(err, mounts.ErrCommandExpired):
		code = "storage_operation_expired"
	case errors.Is(err, mounts.ErrCommandCapacity):
		code = "storage_command_capacity"
	case errors.Is(err, mounts.ErrAllocationCapacity):
		code = "storage_allocation_recovery_required"
	}
	if code != "" {
		return &apierror.Error{Code: code}
	}
	return rcloneConnectionFailure(err)
}

func registerRcloneSourceSave(registry *apikit.Registry, d Dependencies) {
	registerRcloneSourceEdit(registry, d)
	registerRcloneSourceRemoval(registry, d)
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_rclone_operation", Method: "GET", Path: "/v1/storage/sources/rclone/operations/{id}", Summary: "Recover an imported rclone source save receipt", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "administration_unavailable", "storage_command_conflict", "storage_operation_expired", "internal"}}, func(ctx context.Context, r *http.Request, _ struct{}) (RcloneSourceResponse, error) {
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
		receipt, err := d.Mounts.RcloneSourceOperation(ctx, string(actor), r.PathValue("id"), func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		return RcloneSourceResponse{Scope: scope, Receipt: receipt}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "create_storage_source_rclone", Method: "POST", Path: "/v1/storage/sources/rclone", Summary: "Save an imported rclone source using the bundled program", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "source_credentials_required", "administration_unavailable", "storage_command_conflict", "storage_operation_expired", "storage_command_capacity", "storage_allocation_recovery_required", "request_in_progress", "internal"}}, func(ctx context.Context, r *http.Request, in mounts.RcloneSourceInput) (RcloneSourceResponse, error) {
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
		receipt, err := d.Mounts.SaveRcloneSource(ctx, string(actor), in, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		return RcloneSourceResponse{Scope: scope, Receipt: receipt}, nil
	})
}
