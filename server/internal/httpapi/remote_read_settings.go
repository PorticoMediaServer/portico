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
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type SourceReadSettingsResponse struct {
	Scope    catalog.AdminScope               `json:"scope"`
	Settings remotesources.SourceReadSettings `json:"settings"`
}

type SourceReadSettingsReceiptResponse struct {
	Scope   catalog.AdminScope    `json:"scope"`
	Receipt remotesources.Receipt `json:"receipt"`
}

func sourceReadSettingsError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, storage.ErrRemoteChanged):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, mounts.ErrCommandConflict):
		return &apierror.Error{Code: "idempotency_key_reused", Cause: err}
	case errors.Is(err, mounts.ErrUnavailable):
		return &apierror.Error{Code: "administration_unavailable", Cause: err}
	case errors.Is(err, storage.ErrRemoteConfig), errors.Is(err, mounts.ErrInvalid), errors.Is(err, mounts.ErrCommandExpired):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, storage.ErrRemoteOffline):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, storage.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		return &apierror.Error{Code: "request_in_progress", Cause: err}
	default:
		return identityRouteError(err)
	}
}

func registerSourceReadSettings(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "idempotency_key_reused", "request_in_progress", "administration_unavailable", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_read_profile", Method: "GET", Path: "/v1/storage/sources/{id}/settings/read-profile", Summary: "Read one source's normalized reading profile and current revisions", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (SourceReadSettingsResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return SourceReadSettingsResponse{}, sourceReadSettingsError(err)
		}
		if d.RemoteSources == nil {
			return SourceReadSettingsResponse{}, sourceReadSettingsError(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return SourceReadSettingsResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		out, err := d.RemoteSources.ReadSourceReadSettings(ctx, d.Identity, p, r.PathValue("id"))
		if err != nil {
			return SourceReadSettingsResponse{}, sourceReadSettingsError(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return SourceReadSettingsResponse{}, sourceReadSettingsError(err)
		}
		return SourceReadSettingsResponse{Scope: scope, Settings: out}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_storage_source_read_profile", Method: "PUT", Path: "/v1/storage/sources/{id}/settings/read-profile", Summary: "Save one source's reading limits with current revisions and a durable receipt", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, in remotesources.SourceReadSettingsChange) (SourceReadSettingsReceiptResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return SourceReadSettingsReceiptResponse{}, sourceReadSettingsError(err)
		}
		if d.RemoteSources == nil {
			return SourceReadSettingsReceiptResponse{}, sourceReadSettingsError(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return SourceReadSettingsReceiptResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		out, err := d.RemoteSources.SaveSourceReadSettings(ctx, d.Identity, p, r.PathValue("id"), in)
		if err != nil {
			return SourceReadSettingsReceiptResponse{}, sourceReadSettingsError(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return SourceReadSettingsReceiptResponse{}, sourceReadSettingsError(err)
		}
		return SourceReadSettingsReceiptResponse{Scope: scope, Receipt: out}, nil
	})
}
