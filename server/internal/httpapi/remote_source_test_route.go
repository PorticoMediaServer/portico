package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type SourceTestResponse struct {
	Scope  catalog.AdminScope                  `json:"scope"`
	Result remotesources.SavedSourceTestResult `json:"result"`
}

// sourceTestFailure keeps only the existing public codes. A completed provider
// failure is a 200 test result; this mapping is for admission, authorization,
// missing source and local program/config errors.
func sourceTestFailure(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found"}
	case errors.Is(err, remotesources.ErrSavedSourceTestLocal):
		return &apierror.Error{Code: "administration_unavailable"}
	case errors.Is(err, storage.ErrRemoteConfig):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, storage.ErrRemoteOffline):
		return &apierror.Error{Code: "administration_unavailable"}
	case errors.Is(err, storage.ErrRemoteCredentials):
		return &apierror.Error{Code: "source_credentials_required"}
	case errors.Is(err, storage.ErrRemoteBinary), errors.Is(err, mounts.ErrUnavailable):
		return &apierror.Error{Code: "administration_unavailable"}
	case errors.Is(err, storage.ErrBusy), errors.Is(err, remotemedia.ErrBusy):
		return &apierror.Error{Code: "request_in_progress"}
	case errors.Is(err, storage.ErrRemoteChanged):
		return &apierror.Error{Code: "invalid_request"}
	case errors.Is(err, identity.ErrForbidden):
		return &apierror.Error{Code: "not_permitted"}
	case errors.Is(err, identity.ErrUnauthorized):
		return &apierror.Error{Code: "unauthorized"}
	default:
		return &apierror.Error{Code: "internal"}
	}
}

func registerSourceTest(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "test_storage_source", Method: "POST", Path: "/v1/storage/sources/{id}/test", Summary: "Test a saved source's connection without changing it", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "request_in_progress", "source_credentials_required", "administration_unavailable", "internal"}}, func(ctx context.Context, r *http.Request, _ noBody) (SourceTestResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return SourceTestResponse{}, sourceTestFailure(err)
		}
		if d.RemoteSources == nil {
			return SourceTestResponse{}, sourceTestFailure(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return SourceTestResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		result, err := d.RemoteSources.TestSavedSource(ctx, r.PathValue("id"), func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return SourceTestResponse{}, sourceTestFailure(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return SourceTestResponse{}, sourceTestFailure(err)
		}
		return SourceTestResponse{Scope: scope, Result: result}, nil
	})
}
