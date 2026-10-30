package httpapi

import (
	"context"
	"net/http"
	"os"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
)

type EmptyDataCacheResponse struct {
	Scheduled bool `json:"scheduled"`
}

func registerDataStorage(registry *apikit.Registry, d Dependencies) {
	failures := []string{"unauthorized", "not_permitted", "not_found", "internal"}
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "get_data_storage_usage", Method: "GET", Path: "/v1/admin/storage/usage", Summary: "Read data folder usage and storage notices", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (storagepaths.Usage, error) {
		if d.StorageLayout == nil {
			return storagepaths.Usage{}, &apierror.Error{Code: "not_found"}
		}
		// The maintained snapshot the storage care page also reads; a server
		// assembled without it measures directly.
		if d.Administration != nil && d.Administration.DataUsage != nil {
			out, err := d.Administration.DataStorageUsage(ctx)
			if err != nil {
				return out, &apierror.Error{Code: "internal", Cause: err}
			}
			return out, nil
		}
		var mounts []storagepaths.Mount
		if file, err := os.Open("/proc/self/mountinfo"); err == nil {
			mounts = storagepaths.ParseMountInfo(file)
			file.Close()
		}
		out, err := d.StorageLayout.Usage(ctx, mounts)
		if err != nil {
			return out, &apierror.Error{Code: "internal", Cause: err}
		}
		return out, nil
	})
	v1Route(registry, apikit.Metadata{Audience: apikit.Clients, Admission: laneAdmin, ID: "post_empty_data_cache", Method: "POST", Path: "/v1/admin/storage/cache/empty", Summary: "Empty rebuildable cache on a graceful server restart", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Default, Cost: apikit.Constant, Status: 202, Errors: failures}, func(ctx context.Context, r *http.Request, _ noBody) (EmptyDataCacheResponse, error) {
		if d.StorageLayout == nil || d.Restart == nil {
			return EmptyDataCacheResponse{}, &apierror.Error{Code: "not_found"}
		}
		if err := d.StorageLayout.RequestCacheEmpty(); err != nil {
			return EmptyDataCacheResponse{}, &apierror.Error{Code: "internal", Cause: err}
		}
		d.Restart()
		return EmptyDataCacheResponse{Scheduled: true}, nil
	})
}
