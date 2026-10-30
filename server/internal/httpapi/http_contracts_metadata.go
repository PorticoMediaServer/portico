package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	catalog "github.com/PorticoMediaServer/portico/server/internal/catalog"
	metadata "github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func registerMetadataHTTPContracts(registry *apikit.Registry) error {
	// internal/httpapi/metadata_editor.go:25
	if err := apikit.RegisterHTTP[metadata.BulkEdit, metadata.BulkReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_metadata_bulk", Method: "POST", Path: "/v1/metadata/bulk", Summary: "Run metadata bulk", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 524288, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/metadata_editor.go:65
	if err := apikit.RegisterHTTP[struct {
		File             SubtitleBytesContract `json:"file"`
		Subject          string                `json:"subject"`
		ExpectedRevision string                `json:"expectedRevision"`
	}, metadata.RepairState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_metadata_kind_id_art_role_upload", Method: "POST", Path: "/v1/metadata/{kind}/{id}/art/{role}/upload", RequestContentTypes: []string{"multipart/form-data"}, Summary: "Run metadata art upload", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 11534336, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/metadata_editor.go:68
	if err := apikit.RegisterHTTP[struct{}, metadata.RepairState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_metadata_kind_id_art_role_upload_candidateId", Method: "DELETE", Path: "/v1/metadata/{kind}/{id}/art/{role}/upload/{candidateId}", Summary: "Remove metadata art upload", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"expectedRevision"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/metadata_repair.go:77
	if err := apikit.RegisterHTTP[struct{}, metadata.RepairPreview](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_metadata_kind_id_preview", Method: "GET", Path: "/v1/metadata/{kind}/{id}/preview", Summary: "Read metadata preview", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"mergeId"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/metadata_repair.go:102
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_metadata_kind_id_art_role", Method: "GET", Path: "/v1/metadata/{kind}/{id}/art/{role}", Summary: "Read metadata art", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"candidate", "ld", "r", "sd", "size", "subject", "v", "w"}, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/musicbrainz.go:12
	if err := apikit.RegisterHTTP[metadata.LocalAudioPolicyChange, struct {
		EntityId string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_local_audio_policy", Method: "POST", Path: "/v1/items/{id}/metadata/local-audio/policy", Summary: "Run title metadata local audio policy", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:84
	if err := apikit.RegisterHTTP[metadata.ScreenSeasonSelection, struct {
		EntityId string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_shows_id_metadata_screen_season", Method: "PUT", Path: "/v1/shows/{id}/metadata/screen/season", Summary: "Save show metadata screen season", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:103
	if err := apikit.RegisterHTTP[struct{}, metadata.LibraryAgent](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_metadata_agent", Method: "GET", Path: "/v1/libraries/{id}/metadata/agent", Summary: "Read library metadata agent", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:114
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64  "json:\"expectedRevision\""
		Agent            string "json:\"agent\""
	}, metadata.LibraryAgent](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_libraries_id_metadata_agent", Method: "PUT", Path: "/v1/libraries/{id}/metadata/agent", Summary: "Save library metadata agent", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:136
	if err := apikit.RegisterHTTP[struct{}, struct {
		LibraryKind string                        `json:"libraryKind"`
		Agents      []metadata.LibraryAgentOption `json:"agents"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_library_kinds_kind_metadata_agents", Method: "GET", Path: "/v1/library-kinds/{kind}/metadata-agents", Summary: "Read library kinds metadata agents", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:147
	if err := apikit.RegisterHTTP[struct{}, metadata.ScreenPolicy](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_libraries_id_metadata_screen", Method: "GET", Path: "/v1/libraries/{id}/metadata/screen", Summary: "Read library metadata screen", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/screen_metadata.go:162
	if err := apikit.RegisterHTTP[metadata.ScreenPolicyUpdate, struct {
		LibraryId string `json:"libraryId"`
		Status    string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_libraries_id_metadata_screen", Method: "PUT", Path: "/v1/libraries/{id}/metadata/screen", Summary: "Save library metadata screen", Access: apikit.Admin, AdminScope: "libraries", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/tvdb.go:11
	if err := apikit.RegisterHTTP[struct{}, metadata.TVDBState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_shows_id_metadata_tvdb", Method: "GET", Path: "/v1/shows/{id}/metadata/tvdb", Summary: "Read show metadata tvdb", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/tvdb.go:39
	if err := apikit.RegisterHTTP[metadata.TVDBSelection, struct {
		ShowId string `json:"showId"`
		Status string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_shows_id_metadata_tvdb", Method: "PUT", Path: "/v1/shows/{id}/metadata/tvdb", Summary: "Save show metadata tvdb", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/tvdb.go:63
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 "json:\"expectedRevision\""
	}, struct {
		ShowId string `json:"showId"`
		Status string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_shows_id_metadata_tvdb_retry", Method: "POST", Path: "/v1/shows/{id}/metadata/tvdb/retry", Summary: "Run show metadata tvdb retry", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct{}, metadata.MBState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_albums_id_metadata_musicbrainz", Method: "GET", Path: "/v1/albums/{id}/metadata/musicbrainz", Summary: "Read album metadata musicbrainz", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.MBSelection, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_albums_id_metadata_musicbrainz", Method: "PUT", Path: "/v1/albums/{id}/metadata/musicbrainz", Summary: "Save album metadata musicbrainz", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_albums_id_metadata_musicbrainz_retry", Method: "POST", Path: "/v1/albums/{id}/metadata/musicbrainz/retry", Summary: "Run album metadata musicbrainz retry", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_albums_id_metadata_musicbrainz_search", Method: "POST", Path: "/v1/albums/{id}/metadata/musicbrainz/search", Summary: "Run album metadata musicbrainz search", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.MusicPolicyChange, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_albums_id_metadata_musicbrainz_policy", Method: "POST", Path: "/v1/albums/{id}/metadata/musicbrainz/policy", Summary: "Run album metadata musicbrainz policy", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct{}, metadata.MBState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_metadata_musicbrainz", Method: "GET", Path: "/v1/items/{id}/metadata/musicbrainz", Summary: "Read title metadata musicbrainz", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.MBSelection, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_items_id_metadata_musicbrainz", Method: "PUT", Path: "/v1/items/{id}/metadata/musicbrainz", Summary: "Save title metadata musicbrainz", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_musicbrainz_retry", Method: "POST", Path: "/v1/items/{id}/metadata/musicbrainz/retry", Summary: "Run title metadata musicbrainz retry", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_musicbrainz_search", Method: "POST", Path: "/v1/items/{id}/metadata/musicbrainz/search", Summary: "Run title metadata musicbrainz search", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.MusicPolicyChange, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_musicbrainz_policy", Method: "POST", Path: "/v1/items/{id}/metadata/musicbrainz/policy", Summary: "Run title metadata musicbrainz policy", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.ScreenSelection, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_items_id_metadata_screen", Method: "PUT", Path: "/v1/items/{id}/metadata/screen", Summary: "Save title metadata screen", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.ScreenSearch, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_screen_search", Method: "POST", Path: "/v1/items/{id}/metadata/screen/search", Summary: "Run title metadata screen search", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_metadata_screen_retry", Method: "POST", Path: "/v1/items/{id}/metadata/screen/retry", Summary: "Run title metadata screen retry", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.ScreenSelection, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_shows_id_metadata_screen", Method: "PUT", Path: "/v1/shows/{id}/metadata/screen", Summary: "Save show metadata screen", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.ScreenSearch, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_shows_id_metadata_screen_search", Method: "POST", Path: "/v1/shows/{id}/metadata/screen/search", Summary: "Run show metadata screen search", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct {
		ExpectedRevision int64 `json:"expectedRevision"`
	}, struct {
		EntityID string `json:"entityId"`
		Status   string `json:"status"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_shows_id_metadata_screen_retry", Method: "POST", Path: "/v1/shows/{id}/metadata/screen/retry", Summary: "Run show metadata screen retry", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct{}, metadata.RepairState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_metadata_kind_id", Method: "GET", Path: "/v1/metadata/{kind}/{id}", Summary: "Read metadata", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[metadata.RepairCommand, metadata.RepairState](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_metadata_kind_id", Method: "POST", Path: "/v1/metadata/{kind}/{id}", Summary: "Run metadata", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 1048576, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[struct{}, catalog.ManualMetadata](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_metadata_manual", Method: "GET", Path: "/v1/items/{id}/metadata/manual", Summary: "Read title metadata manual", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/manual_metadata.go:53
	if err := apikit.RegisterHTTP[catalog.ManualMetadataMutation, catalog.ManualMetadata](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_items_id_metadata_manual", Method: "PATCH", Path: "/v1/items/{id}/metadata/manual", Summary: "Change title metadata manual", Access: apikit.Admin, AdminScope: "metadata", Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 262144, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
