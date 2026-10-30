package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
)

// RcloneConfigRemotesResponse is the server account's own rclone configuration
// reduced to remote names, declared types and why each can or cannot be used.
type RcloneConfigRemotesResponse struct {
	Scope     catalog.AdminScope    `json:"scope"`
	Path      string                `json:"path"`
	Remotes   []mounts.ConfigRemote `json:"remotes"`
	Truncated bool                  `json:"truncated"`
}

// RcloneConfigSourceInput names one remote of that configuration instead of
// sending its configuration section from the browser.
type RcloneConfigSourceInput struct {
	OperationID string `json:"operationId"`
	Name        string `json:"name"`
	Remote      string `json:"remote"`
}

func registerRcloneConfigSource(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_storage_source_rclone_config", Method: "GET", Path: "/v1/storage/sources/rclone/config", Summary: "Read the server account's own rclone remotes by name and type", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "administration_unavailable", "internal"}}, func(_ context.Context, r *http.Request, _ struct{}) (RcloneConfigRemotesResponse, error) {
		_, scope, err := d.storageOwner(r)
		if err != nil {
			return RcloneConfigRemotesResponse{}, rcloneConnectionFailure(err)
		}
		if d.Mounts == nil || d.RemoteSources == nil {
			return RcloneConfigRemotesResponse{}, rcloneConnectionFailure(mounts.ErrUnavailable)
		}
		if r.URL.RawQuery != "" {
			return RcloneConfigRemotesResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		path, remotes, truncated, err := mounts.ReadAccountRcloneConfig()
		if err != nil {
			return RcloneConfigRemotesResponse{}, rcloneConnectionFailure(mounts.ErrUnavailable)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return RcloneConfigRemotesResponse{}, rcloneConnectionFailure(err)
		}
		if remotes == nil {
			remotes = []mounts.ConfigRemote{}
		}
		return RcloneConfigRemotesResponse{Scope: scope, Path: path, Remotes: remotes, Truncated: truncated}, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, ID: "create_storage_source_rclone_config", Method: "POST", Path: "/v1/storage/sources/rclone/config", Summary: "Save an imported source from the server account's own rclone configuration", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Admission: laneAdminLong, Cost: apikit.Constant, BodyLimit: 4096, Status: 200, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "not_found", "source_credentials_required", "administration_unavailable", "storage_command_conflict", "storage_operation_expired", "storage_command_capacity", "storage_allocation_recovery_required", "internal"}}, func(ctx context.Context, r *http.Request, in RcloneConfigSourceInput) (RcloneSourceResponse, error) {
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
		name := strings.TrimSpace(in.Name)
		if name == "" || utf8.RuneCountInString(name) > 200 || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return RcloneSourceResponse{}, &apierror.Error{Code: "invalid_request"}
		}
		actor, _ := json.Marshal([]string{p.Authority, p.AccountID, p.ProfileID})
		receipt, err := d.Mounts.SaveRcloneConfigSource(ctx, string(actor), mounts.RcloneConfigSourceInput{OperationID: in.OperationID, Name: name, Remote: in.Remote}, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		if err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		if _, _, err = d.storageOwner(r); err != nil {
			return RcloneSourceResponse{}, rcloneSourceFailure(err)
		}
		return RcloneSourceResponse{Scope: scope, Receipt: receipt}, nil
	})
}
