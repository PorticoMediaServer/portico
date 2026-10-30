package httpapi

import (
	"context"
	"net/http"

	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

func registerStorageStatus(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "invalid_request", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_storage_status", Method: "GET", Path: "/v1/admin/storage/status", Summary: "Read active storage destinations and reclaimable database space", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (persistence.StorageStatus, error) {
		out, err := persistence.ReadStorageStatus(ctx, d.DB)
		out.CacheDirectory = d.CacheDirectory
		if d.Backups != nil {
			out.BackupDirectory = d.Backups.Directory()
		}
		return out, err
	})
}
