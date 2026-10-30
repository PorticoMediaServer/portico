package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

type SourceFoldersResponse struct {
	Scope   catalog.AdminScope             `json:"scope"`
	Folders remotesources.SourceFolderPage `json:"folders"`
}

// folderError keeps the explorer's failure in plain, actionable codes: an
// unreachable storage is retryable, refused credentials need the owner, and a
// missing source is gone rather than a server fault.
func folderError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, identity.ErrUnauthorized):
		return &apierror.Error{Code: "unauthorized", Cause: err}
	case errors.Is(err, identity.ErrForbidden):
		return &apierror.Error{Code: "not_permitted", Cause: err}
	case errors.Is(err, storage.ErrRootedUnsupported):
		return &apierror.Error{Code: "source_folders_unsupported", Cause: err}
	case errors.Is(err, storage.ErrRemoteCredentials):
		return &apierror.Error{Code: "source_credentials_required", Cause: err}
	case errors.Is(err, storage.ErrRemoteOffline), errors.Is(err, sql.ErrNoRows):
		return &apierror.Error{Code: "not_found", Cause: err}
	case errors.Is(err, storage.ErrRemoteConfig), errors.Is(err, storage.ErrRemoteRange):
		return &apierror.Error{Code: "invalid_request", Cause: err}
	case errors.Is(err, storage.ErrRemoteChanged):
		return &apierror.Error{Code: "revision_mismatch", Cause: err}
	case errors.Is(err, storage.ErrBusy), errors.Is(err, context.DeadlineExceeded):
		return &apierror.Error{Code: "request_in_progress", Cause: err}
	case errors.Is(err, storage.ErrRemoteLimit):
		return &apierror.Error{Code: "request_in_progress", Cause: err}
	case errors.Is(err, mounts.ErrUnavailable):
		return &apierror.Error{Code: "administration_unavailable", Cause: err}
	default:
		return &apierror.Error{Code: "source_unavailable", Cause: err}
	}
}

// folderLimit bounds one page of folders. The drawn explorer shows 500 at a
// time; the client asks for at most that.
func folderLimit(raw string) (int, error) {
	if raw == "" {
		return 200, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 1 || n > 500 {
		return 0, &apierror.Error{Code: "invalid_request"}
	}
	return n, nil
}

func registerSourceFolders(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "revision_mismatch", "request_in_progress", "source_credentials_required", "source_unavailable", "source_folders_unsupported", "administration_unavailable", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_folders", Method: "GET", Path: "/v1/storage/sources/{id}/folders", Summary: "List one folder inside a saved remote source, folders only", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, Query: []string{"path", "cursor", "limit", "showHidden"}, QueryIntegers: emptyLimitQuery(200, 500), Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (SourceFoldersResponse, error) {
		p, scope, err := d.storageOwner(r)
		if err != nil {
			return SourceFoldersResponse{}, folderError(err)
		}
		if d.RemoteSources == nil {
			return SourceFoldersResponse{}, folderError(mounts.ErrUnavailable)
		}
		query := r.URL.Query()
		for key := range query {
			switch key {
			case "path", "cursor", "limit", "showHidden":
			default:
				return SourceFoldersResponse{}, &apierror.Error{Code: "invalid_request"}
			}
		}
		limit, err := folderLimit(query.Get("limit"))
		if err != nil {
			return SourceFoldersResponse{}, err
		}
		hidden := query.Get("showHidden")
		if hidden != "" && hidden != "1" && hidden != "true" && hidden != "0" && hidden != "false" {
			return SourceFoldersResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		folders, err := d.RemoteSources.ListSourceFolders(ctx, d.Identity, p, r.PathValue("id"), query.Get("path"), hidden == "1" || hidden == "true", query.Get("cursor"), limit)
		if err != nil {
			return SourceFoldersResponse{}, folderError(err)
		}
		return SourceFoldersResponse{Scope: scope, Folders: folders}, nil
	})
}
