package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	catalog "github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func registerPersonalHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, catalog.SavedSummary](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_me_saved_summary", Method: "GET", Path: "/v1/me/saved/summary", Summary: "Read private Saved landing counts", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, catalog.RemovedTitle](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_me_removed_titles_id", Method: "GET", Path: "/v1/me/removed-titles/{id}", Summary: "Read this profile's removed-title reference", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, struct {
		Removed bool `json:"removed"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_me_removed_titles_id", Method: "DELETE", Path: "/v1/me/removed-titles/{id}", Summary: "Remove this profile's removed-title Continue reference", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/discovery.go:144
	if err := apikit.RegisterHTTP[struct{}, struct {
		Categories []catalog.Category `json:"categories"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_categories", Method: "GET", Path: "/v1/libraries/{id}/categories", Summary: "Read library categories", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:166
	if err := apikit.RegisterHTTP[struct{}, struct {
		Collections []catalog.Collection `json:"collections"`
		NextCursor  string               `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_collections", Method: "GET", Path: "/v1/libraries/{id}/collections", Summary: "Read library collections", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:189
	if err := apikit.RegisterHTTP[struct {
		Name string "json:\"name\""
	}, catalog.Collection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_libraries_id_collections", Method: "POST", Path: "/v1/libraries/{id}/collections", Summary: "Run library collections", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 201, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:239
	if err := apikit.RegisterHTTP[struct{}, catalog.Collection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_collections_id", Method: "GET", Path: "/v1/collections/{id}", Summary: "Read collections", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:267
	if err := apikit.RegisterHTTP[struct {
		Name string "json:\"name\""
	}, catalog.Collection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_collections_id", Method: "PATCH", Path: "/v1/collections/{id}", Summary: "Change collections", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:286
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_collections_id", Method: "DELETE", Path: "/v1/collections/{id}", Summary: "Remove collections", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:298
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_collections_id_items_itemId", Method: "PUT", Path: "/v1/collections/{id}/items/{itemId}", Summary: "Save collections title", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/discovery.go:298
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_collections_id_items_itemId", Method: "DELETE", Path: "/v1/collections/{id}/items/{itemId}", Summary: "Remove collections title", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:28
	if err := apikit.RegisterHTTP[struct{}, catalog.Playlist](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_playlists_id", Method: "GET", Path: "/v1/playlists/{id}", Summary: "Read playlists", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:50
	if err := apikit.RegisterHTTP[struct{}, catalog.ContentEnvelope](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_playlists_id_content", Method: "GET", Path: "/v1/playlists/{id}/content", Summary: "Read playlists content", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "sort", "direction", "filter"}, QueryIntegers: limitQuery(40, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "invalid_cursor", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:93
	if err := apikit.RegisterHTTP[struct{}, catalog.PlaylistCandidates](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_playlists_id_share_candidates", Method: "GET", Path: "/v1/playlists/{id}/share-candidates", Summary: "Read playlists share candidates", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: limitQuery(40, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "invalid_cursor", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_playlists", Method: "POST", Path: "/v1/playlists", Summary: "Run playlists", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_playlists_id", Method: "PATCH", Path: "/v1/playlists/{id}", Summary: "Change playlists", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_playlists_id", Method: "DELETE", Path: "/v1/playlists/{id}", Summary: "Remove playlists", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_playlists_id_entries", Method: "POST", Path: "/v1/playlists/{id}/entries", Summary: "Run playlists entries", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "operation_expired", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_playlists_id_entries_entryId", Method: "DELETE", Path: "/v1/playlists/{id}/entries/{entryId}", Summary: "Remove playlists entries", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_playlists_id_shares", Method: "PUT", Path: "/v1/playlists/{id}/shares", Summary: "Save playlists shares", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playlists.go:131
	if err := apikit.RegisterHTTP[catalog.PlaylistMutation, catalog.PlaylistReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_playlists_id_shares", Method: "DELETE", Path: "/v1/playlists/{id}/shares", Summary: "Remove playlists shares", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "playlist_capacity", "idempotency_key_reused", "rate_limited", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:55
	if err := apikit.RegisterHTTP[struct{}, catalog.SavedResourceCardsPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_saved_resources", Method: "GET", Path: "/v1/saved-resources", Summary: "Read saved resources", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "kind", "pinned", "shape", "limit"}, QueryIntegers: limitQuery(40, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:81
	if err := apikit.RegisterHTTP[struct{}, catalog.SavedResource](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_saved_resources_id", Method: "GET", Path: "/v1/saved-resources/{id}", Summary: "Read saved resources", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:121
	if err := apikit.RegisterHTTP[struct{}, catalog.SavedResourceCandidates](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_saved_resources_id_share_candidates", Method: "GET", Path: "/v1/saved-resources/{id}/share-candidates", Summary: "Read saved resources share candidates", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: limitQuery(40, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_saved_resources", Method: "POST", Path: "/v1/saved-resources", Summary: "Run saved resources", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_saved_resources_id", Method: "PATCH", Path: "/v1/saved-resources/{id}", Summary: "Change saved resources", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_saved_resources_id", Method: "DELETE", Path: "/v1/saved-resources/{id}", Summary: "Remove saved resources", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_saved_resources_id_entries", Method: "PUT", Path: "/v1/saved-resources/{id}/entries", Summary: "Save saved resources entries", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "reorder_saved_collection", Method: "PUT", Path: "/v1/saved-resources/{id}/order", Summary: "Move entries of a personal collection (at most 200), fenced by its revision", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_saved_resources_id_shares", Method: "PUT", Path: "/v1/saved-resources/{id}/shares", Summary: "Save saved resources shares", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:140
	if err := apikit.RegisterHTTP[catalog.SavedResourceMutation, struct {
		ServerID    string                       `json:"serverId"`
		ViewerFence string                       `json:"viewerFence"`
		Receipt     catalog.SavedResourceReceipt `json:"receipt"`
		Current     *struct {
			ResourceID string `json:"resourceId"`
			Revision   int64  `json:"revision"`
			Deleted    bool   `json:"deleted"`
		} `json:"current,omitempty"`
		ReceiptLifetimeMs int64 `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_saved_resources_id_shares", Method: "DELETE", Path: "/v1/saved-resources/{id}/shares", Summary: "Remove saved resources shares", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:221
	if err := apikit.RegisterHTTP[catalog.PinMutation, struct {
		ServerID    string `json:"serverId"`
		ViewerFence string `json:"viewerFence"`
		OperationID string `json:"operationId"`
		ResourceID  string `json:"resourceId"`
		PlaylistID  string `json:"playlistId"`
		Revision    int64  `json:"revision"`
		PinRevision int64  `json:"pinRevision"`
		Pinned      bool   `json:"pinned"`
		Deleted     bool   `json:"deleted"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_saved_pins_kind_id", Method: "PUT", Path: "/v1/saved-pins/{kind}/{id}", Summary: "Save saved pins", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:240
	if err := apikit.RegisterHTTP[catalog.RecommendationsReset, struct {
		ServerID             string `json:"serverId"`
		ViewerFence          string `json:"viewerFence"`
		OperationID          string `json:"operationId"`
		ClearedNotInterested int    `json:"clearedNotInterested"`
		ReceiptLifetimeMs    int64  `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_me_recommendations_reset", Method: "POST", Path: "/v1/me/recommendations:reset", Summary: "Run viewer recommendations reset", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/saved_resources.go:258
	if err := apikit.RegisterHTTP[catalog.ActivityMutation, struct {
		ServerID          string                  `json:"serverId"`
		ViewerFence       string                  `json:"viewerFence"`
		Receipt           catalog.ActivityReceipt `json:"receipt"`
		ReceiptLifetimeMs int64                   `json:"receiptLifetimeMs"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_personal_history_actions", Method: "POST", Path: "/v1/personal-history/actions", Summary: "Run personal history actions", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
