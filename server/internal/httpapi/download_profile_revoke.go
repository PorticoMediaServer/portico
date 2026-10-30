package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
)

type revokeProfileDownloads struct {
	ProfileKey  string `json:"profileKey"`
	OperationID string `json:"operationId"`
}

func registerDownloadProfileRevoke(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneSecurityFence, ID: "revoke_profile_downloads", Method: "POST", Path: "/v1/admin/downloads/revoke", Summary: "Withdraw one bounded page of a profile’s download claims and offline permissions; repeat while more is true", Access: apikit.Owner, Requires: apikit.ScopePlayback, Lane: apikit.Security, Cost: apikit.PageSized, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "idempotency_key_reused", "downloads_unavailable", "internal"}}, func(ctx context.Context, r *http.Request, in revokeProfileDownloads) (downloads.ProfileRevocation, error) {
		p, err := d.owner(r)
		if err != nil {
			return downloads.ProfileRevocation{}, err
		}
		if d.Downloads == nil {
			return downloads.ProfileRevocation{}, errDownloadsUnavailable
		}
		out, err := d.Downloads.RevokeProfile(ctx, p, in.ProfileKey, in.OperationID, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		return out, downloadError(err)
	})
}
