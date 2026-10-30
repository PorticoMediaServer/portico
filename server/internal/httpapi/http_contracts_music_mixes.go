package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func registerMusicMixesHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, projectedViewerEnvelope[MusicMixesView]](registry, apikit.Metadata{Audience: apikit.Clients,
		ID: "get_music_mixes", Method: "GET", Path: "/v1/music/mixes", Summary: "Read the current profile's permitted listening mixes",
		Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneExpensive, Cost: apikit.PageSized, Status: 200,
		Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "internal"},
	}); err != nil {
		return err
	}
	return apikit.RegisterHTTP[musicMixSaveChange, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients,
		ID: "post_music_mixes_id_playlist", Method: "POST", Path: "/v1/music/mixes/{id}/playlist", Summary: "Copy a permitted mix into an ordinary playlist",
		Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 4096, Status: 200,
		Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "playlist_capacity", "idempotency_key_reused", "internal"},
	})
}
