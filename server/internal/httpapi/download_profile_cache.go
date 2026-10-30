package httpapi

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/downloads"
)

type releaseProfileDownloadsCache struct {
	ProfileKey  string `json:"profileKey"`
	OperationID string `json:"operationId"`
}

func registerDownloadProfileCache(registry *apikit.Registry, d Dependencies) {
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "release_profile_downloads_cache", Method: "POST", Path: "/v1/admin/downloads/cache/release", Summary: "Release one bounded page of a profile’s prepared download copies without withdrawing offline permissions; repeat while more is true", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, BodyLimit: 4096, Errors: []string{"unauthorized", "not_permitted", "invalid_request", "idempotency_key_reused", "downloads_unavailable", "internal"}}, func(ctx context.Context, r *http.Request, in releaseProfileDownloadsCache) (downloads.ProfileCacheRelease, error) {
		p, err := d.owner(r)
		if err != nil {
			return downloads.ProfileCacheRelease{}, err
		}
		if d.Downloads == nil {
			return downloads.ProfileCacheRelease{}, errDownloadsUnavailable
		}
		out, err := d.Downloads.ReleaseProfileCache(ctx, p, in.ProfileKey, in.OperationID, func(tx *sql.Tx) error { return d.resourceAuthorize(tx, p, "") })
		return out, downloadError(err)
	})
}
