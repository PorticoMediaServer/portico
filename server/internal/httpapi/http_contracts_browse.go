package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	catalog "github.com/PorticoMediaServer/portico/server/internal/catalog"
	operations "github.com/PorticoMediaServer/portico/server/internal/operations"
)

func registerBrowseHTTPContracts(registry *apikit.Registry) error {
	if err := apikit.RegisterHTTP[struct{}, genreReadResponse](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_search_genres", Method: "GET", Path: "/v1/search/genres", Summary: "Read permitted video genres with selected representative artwork", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneExpensive, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: genrePreparingHeaders(), Query: []string{"limit", "cursor", "preview"}, QueryIntegers: limitQuery(catalog.SearchGenresMaximumLimit, catalog.SearchGenresMaximumLimit), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "search_busy", "metadata_unavailable", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/audio.go:11
	if err := apikit.RegisterHTTP[struct{}, struct {
		Artists    []catalog.Artist `json:"artists"`
		NextCursor string           `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_artists", Method: "GET", Path: "/v1/libraries/{id}/artists", Summary: "Read library artist", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio.go:33
	if err := apikit.RegisterHTTP[struct{}, struct {
		Books      []catalog.Book `json:"books"`
		NextCursor string         `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_books", Method: "GET", Path: "/v1/libraries/{id}/books", Summary: "Read library book", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio.go:56
	if err := apikit.RegisterHTTP[struct{}, struct {
		Albums     []catalog.Album `json:"albums"`
		NextCursor string          `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_artists_id_albums", Method: "GET", Path: "/v1/artists/{id}/albums", Summary: "Read artist album", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio.go:56
	if err := apikit.RegisterHTTP[struct{}, struct {
		Songs      []catalog.Item `json:"songs"`
		NextCursor string         `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_albums_id_songs", Method: "GET", Path: "/v1/albums/{id}/songs", Summary: "Read album songs", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio.go:56
	if err := apikit.RegisterHTTP[struct{}, struct {
		Files      []catalog.Item `json:"files"`
		NextCursor string         `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_books_id_files", Method: "GET", Path: "/v1/books/{id}/files", Summary: "Read book files", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio.go:56
	if err := apikit.RegisterHTTP[struct{}, struct {
		Chapters   []catalog.BookChapter `json:"chapters"`
		NextCursor string                `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_books_id_chapters", Method: "GET", Path: "/v1/books/{id}/chapters", Summary: "Read book chapters", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/browse_routes.go:79
	if err := apikit.RegisterHTTP[struct{}, catalog.BrowseCapabilities](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_browse_capabilities", Method: "GET", Path: "/v1/libraries/{id}/browse-capabilities", Summary: "Read library browse capabilities", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"pivot"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/browse_routes.go:102
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64                   "json:\"expectedRevision\""
		Order            []catalog.PinOrderEntry "json:\"order\""
	}, struct {
		ServerID    string                  `json:"serverId"`
		ViewerFence string                  `json:"viewerFence"`
		Revision    int64                   `json:"revision"`
		Order       []catalog.PinOrderEntry `json:"order"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_saved_pins_order", Method: "PUT", Path: "/v1/saved-pins/order", Summary: "Save saved pins order", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/browse_routes.go:125
	if err := apikit.RegisterHTTP[struct{}, struct {
		ServerID    string                   `json:"serverId"`
		ViewerFence string                   `json:"viewerFence"`
		Revision    int64                    `json:"revision"`
		Order       []catalog.PinnedResource `json:"order"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_saved_pins_order", Method: "GET", Path: "/v1/saved-pins/order", Summary: "Read saved pins order", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/browse_routes.go:138
	if err := apikit.RegisterHTTP[struct{}, catalog.LibraryNavigation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_me_library_navigation", Method: "GET", Path: "/v1/me/library-navigation", Summary: "Read viewer library navigation", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/browse_routes.go:155
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64    "json:\"expectedRevision\""
		PinnedLibraryIDs []string "json:\"pinnedLibraryIds\""
	}, catalog.LibraryNavigation](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_me_library_navigation", Method: "PUT", Path: "/v1/me/library-navigation", Summary: "Save viewer library navigation", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/detail.go:134
	if err := apikit.RegisterHTTP[struct{}, catalog.CreditPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_credits", Method: "GET", Path: "/v1/items/{id}/credits", Summary: "Read title credits", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "group", "limit"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 200, Maximum: 200, DefaultBelowMinimum: true, DefaultAboveMaximum: true, DefaultOnInvalid: true, EmptyIsDefault: true, Description: "Absent, empty, invalid syntax and out-of-range values use the 200-credit page; integer overflow saturates before normalization."}}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:36
	if err := apikit.RegisterHTTP[struct{}, struct {
		Shows      []catalog.Show `json:"shows"`
		NextCursor string         `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_shows", Method: "GET", Path: "/v1/libraries/{id}/shows", Summary: "Read library show", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:60
	if err := apikit.RegisterHTTP[struct{}, struct {
		Seasons []catalog.Season `json:"seasons"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_shows_id_seasons", Method: "GET", Path: "/v1/shows/{id}/seasons", Summary: "Read show season", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:78
	if err := apikit.RegisterHTTP[struct{}, struct {
		Episodes   []catalog.Item `json:"episodes"`
		NextCursor string         `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_seasons_id_episodes", Method: "GET", Path: "/v1/seasons/{id}/episodes", Summary: "Read season episodes", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "playState"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:97
	if err := apikit.RegisterHTTP[struct{}, struct {
		Episodes   []catalog.Item `json:"episodes"`
		NextCursor string         `json:"nextCursor"`
		Group      string         `json:"group"`
	}](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_shows_id_episodes", Method: "GET", Path: "/v1/shows/{id}/episodes", Summary: "Read show episodes", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "playState"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:116
	if err := apikit.RegisterHTTP[struct{}, struct {
		ShowId   string                  `json:"showId"`
		Rows     []catalog.HomeRow       `json:"rows"`
		Revision catalog.ContentRevision `json:"revision"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_shows_id_recommendations", Method: "GET", Path: "/v1/shows/{id}/recommendations", Summary: "Read show recommendations", Query: []string{"limit"}, QueryIntegers: []apikit.QueryInteger{homeLimitQuery}, Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneExpensive, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:154
	if err := apikit.RegisterHTTP[struct{}, struct {
		Issues     []catalog.EpisodeIssue `json:"issues"`
		NextCursor string                 `json:"nextCursor"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_episode_issues", Method: "GET", Path: "/v1/libraries/{id}/episode-issues", Summary: "Read library episode issues", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/episodes.go:167
	if err := apikit.RegisterHTTP[catalog.EpisodeAssignment, struct {
		Status string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_libraries_id_episode_assignments", Method: "POST", Path: "/v1/libraries/{id}/episode-assignments", Summary: "Run library episode assignments", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/home_routes.go:99
	if err := apikit.RegisterHTTP[struct{}, catalog.HomeLayoutView](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_home_layout", Method: "GET", Path: "/v1/home/layout", Summary: "Read home layout", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/home_routes.go:121
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64    `json:"expectedRevision"`
		IdempotencyKey   string   `json:"idempotencyKey"`
		RowOrder         []string `json:"rowOrder"`
		HiddenRowIDs     []string `json:"hiddenRowIds"`
	}, operations.HomeLayoutDocument](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_home_layout", Method: "PUT", Path: "/v1/home/layout", Summary: "Save home layout", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "home_layout_conflict", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/home_routes.go:124
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64    `json:"expectedRevision"`
		IdempotencyKey   string   `json:"idempotencyKey"`
		RowOrder         []string `json:"rowOrder"`
		HiddenRowIDs     []string `json:"hiddenRowIds"`
	}, operations.HomeLayoutDocument](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_home_layout_reset", Method: "POST", Path: "/v1/home/layout/reset", Summary: "Run home layout reset", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/home_routes.go:127
	if err := apikit.RegisterHTTP[struct{}, catalog.Suggestions](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_suggestions", Method: "GET", Path: "/v1/suggestions", Summary: "Read suggestions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneExpensive, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"limit"}, QueryIntegers: []apikit.QueryInteger{homeLimitQuery}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/home_routes.go:164
	if err := apikit.RegisterHTTP[struct{}, struct {
		ItemId   string                  `json:"itemId"`
		Rows     []catalog.HomeRow       `json:"rows"`
		Revision catalog.ContentRevision `json:"revision"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_recommendations", Method: "GET", Path: "/v1/items/{id}/recommendations", Summary: "Read title recommendations", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneExpensive, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"limit"}, QueryIntegers: []apikit.QueryInteger{homeLimitQuery}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	if err := apikit.RegisterHTTP[struct{}, projectedViewerEnvelope[SonicSimilarView]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_similar", Method: "GET", Path: "/v1/items/{id}/similar", Summary: "Read locally ranked permitted listening titles", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"limit", "by", "cursor"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 24, Maximum: 100, ClampMaximum: 24, EmptyIsDefault: true}}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "stale_continuation", "internal"}}); err != nil {
		return err
	}

	if err := apikit.RegisterHTTP[struct{}, projectedViewerEnvelope[catalog.BookTimeline]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_books_id_timeline", Method: "GET", Path: "/v1/books/{id}/timeline", Summary: "Read verified whole-book timeline and seek targets", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "positionMs"}, QueryIntegers: legacyBrowseLimitQuery, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}

	// internal/httpapi/listening.go:17
	if err := apikit.RegisterHTTP[struct{}, projectedViewerEnvelope[catalog.ListeningPreferences]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_listening_preferences", Method: "GET", Path: "/v1/listening/preferences", Summary: "Read listening preferences", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/listening.go:31
	if err := apikit.RegisterHTTP[struct {
		Revision       *int64   "json:\"revision\""
		BookRate       *float64 "json:\"bookRate\""
		PassoutMinutes *int     "json:\"passoutMinutes\""
	}, projectedViewerEnvelope[catalog.ListeningPreferences]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_listening_preferences", Method: "PUT", Path: "/v1/listening/preferences", Summary: "Save listening preferences", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/listening.go:97
	if err := apikit.RegisterHTTP[struct{}, projectedViewerEnvelope[catalog.ListeningItem]](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_listening", Method: "GET", Path: "/v1/items/{id}/listening", Summary: "Read title listening", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/people_routes.go:18
	if err := apikit.RegisterHTTP[struct{}, catalog.PeopleDirectory](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_people", Method: "GET", Path: "/v1/people", Summary: "Read people", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"limit", "q"}, QueryIntegers: limitQuery(25, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/people_routes.go:86
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_people_id_portrait", Method: "GET", Path: "/v1/people/{id}/portrait", Summary: "Read people portrait", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"v"}, ResponseContentType: "image/jpeg", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/people_routes.go: a library collection's manual order
	if err := apikit.RegisterHTTP[collectionReorder, catalog.Collection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "reorder_library_collection", Method: "PUT", Path: "/v1/collections/{id}/order", Summary: "Move titles of a library collection (at most 200), fenced by its revision", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_mismatch", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/people_routes.go:195
	if err := apikit.RegisterHTTP[struct {
		AddItemIDs    []string "json:\"addItemIds\""
		RemoveItemIDs []string "json:\"removeItemIds\""
	}, catalog.CollectionBatch](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_collections_id_items_batch", Method: "POST", Path: "/v1/collections/{id}/items:batch", Summary: "Run collections items batch", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/search.go:62
	if err := apikit.RegisterHTTP[struct{}, catalog.SearchHistoryPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_search_history", Method: "GET", Path: "/v1/search/history", Summary: "Read search history", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneExpensive, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/search.go:80
	if err := apikit.RegisterHTTP[struct{}, catalog.SearchHistoryPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_search_history", Method: "DELETE", Path: "/v1/search/history", Summary: "Remove search history", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneExpensive, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"query"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/show_workspace.go:11
	if err := apikit.RegisterHTTP[struct{}, catalog.ShowWorkspace](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_show_workspace", Method: "GET", Path: "/v1/libraries/{id}/show-workspace", Summary: "Read library show workspace", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"episodeDirection", "playState", "cursor", "episodeId", "group", "seasonCursor", "seasonId", "selectedSeasonId", "showId"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
