package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	catalog "github.com/PorticoMediaServer/portico/server/internal/catalog"
	hosted "github.com/PorticoMediaServer/portico/server/internal/hosted"
	identity "github.com/PorticoMediaServer/portico/server/internal/identity"
	ingestion "github.com/PorticoMediaServer/portico/server/internal/ingestion"
	lyrics "github.com/PorticoMediaServer/portico/server/internal/lyrics"
	mediaanalysis "github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	operations "github.com/PorticoMediaServer/portico/server/internal/operations"
	playback "github.com/PorticoMediaServer/portico/server/internal/playback"
	preparedmedia "github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"reflect"
)

// AnalysisControlResponse describes the existing flat responses: a queued job
// for retry, or an acknowledgement for pause, resume and cancel.
type AnalysisControlResponse struct{}

func (AnalysisControlResponse) JSONSchema() map[string]any {
	job, _ := apikit.Schema(reflect.TypeFor[ingestion.Job]())
	acknowledgement, _ := apikit.Schema(reflect.TypeFor[struct {
		Status string `json:"status"`
	}]())
	// The fuller shape comes first so tolerant decoding preserves job fields.
	return map[string]any{"anyOf": []any{job, acknowledgement}}
}

func registerPlaybackHTTPContracts(registry *apikit.Registry) error {
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[struct{}, playback.ActivityPage](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_playback_sessions", Method: "GET", Path: "/v1/admin/playback-sessions", Summary: "Read server playback sessions", Access: apikit.Owner, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "status"}, QueryIntegers: limitQuery(40, 40), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[playback.ActivityStop, playback.ActivityReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_admin_playback_sessions_id_stop", Method: "POST", Path: "/v1/admin/playback-sessions/{id}/stop", Summary: "Run server playback sessions stop", Access: apikit.Owner, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneAdminHeavy, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[struct{}, playback.ActivityReceipt](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_playback_operations_operationId", Method: "GET", Path: "/v1/admin/playback-operations/{operationId}", Summary: "Read server playback operations", Access: apikit.Owner, Requires: apikit.ScopePlayback, Lane: apikit.Default, Admission: laneAdmin, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio_media.go:133
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_grant_audio", Method: "GET", Path: "/v1/media/{grant}/audio", Summary: "Read media audio", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio_media.go:134
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "head_media_grant_audio", Method: "HEAD", Path: "/v1/media/{grant}/audio", Summary: "Read headers for media audio", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio_media.go: the seek index of a direct MP3, ADTS or Ogg plan
	if err := apikit.RegisterHTTP[struct{}, SeekIndexContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_grant_audio_index", Method: "GET", Path: "/v1/media/{grant}/audio-index", Summary: "Read the seek index of media audio", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/audio_media.go:137
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_grant_audio_converted", Method: "GET", Path: "/v1/media/{grant}/audio-converted", Summary: "Read media audio converted", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"fromFrame"}, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/delivery_routes.go:68
	if err := apikit.RegisterHTTP[struct{}, struct {
		ServerId         string                          `json:"serverId"`
		Policy           playback.ResolvedDeliveryPolicy `json:"policy"`
		Ladder           []playback.QualityPreset        `json:"ladder"`
		PlanningPolicies []string                        `json:"planningPolicies"`
		TransportClasses []string                        `json:"transportClasses"`
		SavedQualities   []string                        `json:"savedQualities"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_playback_delivery_policy", Method: "GET", Path: "/v1/playback/delivery-policy", Summary: "Read playback delivery policy", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: lanePlayback, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/linear_media.go:83
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_linear_playbackId_generation_token_name", Method: "GET", Path: "/v1/media/linear/{playbackId}/{generation}/{token}/{name}", Summary: "Read media linear", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[struct{}, lyrics.View](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_lyrics", Method: "GET", Path: "/v1/items/{id}/lyrics", Summary: "Read title lyrics", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"sessionId", "sourceId", "sourceVersion"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[struct{}, lyrics.Revision](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_lyrics_resource_revisions_revision", Method: "GET", Path: "/v1/items/{id}/lyrics/{resource}/revisions/{revision}", Summary: "Read title lyrics revisions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"sessionId", "sourceId", "sourceVersion"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[lyrics.Mutation, lyrics.Revision](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_lyrics", Method: "POST", Path: "/v1/items/{id}/lyrics", Summary: "Run title lyrics", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[lyrics.Mutation, lyrics.Revision](registry, apikit.Metadata{Audience: apikit.Clients, ID: "patch_items_id_lyrics_resource_offset", Method: "PATCH", Path: "/v1/items/{id}/lyrics/{resource}/offset", Summary: "Change title lyrics offset", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[lyrics.Mutation, lyrics.Revision](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_items_id_lyrics_resource", Method: "DELETE", Path: "/v1/items/{id}/lyrics/{resource}", Summary: "Remove title lyrics", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[lyrics.SearchInput, lyrics.SearchResult](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_lyrics_search", Method: "POST", Path: "/v1/items/{id}/lyrics/search", Summary: "Run title lyrics search", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:118
	if err := apikit.RegisterHTTP[lyrics.Choose, lyrics.Selection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_items_id_lyrics_selection", Method: "PUT", Path: "/v1/items/{id}/lyrics/selection", Summary: "Save title lyrics selection", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/lyrics.go:262
	if err := apikit.RegisterHTTP[struct {
		Enabled          bool  "json:\"enabled\""
		ExpectedRevision int64 "json:\"expectedRevision\""
	}, lyrics.ProviderSettings](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_lyrics_provider", Method: "PUT", Path: "/v1/lyrics/provider", Summary: "Save lyrics provider", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2 << 20, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[struct{}, mediaanalysis.View](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_analysis", Method: "GET", Path: "/v1/items/{id}/analysis", Summary: "Read title analysis", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"sourceId", "sourceRevision", "mappingRevision", "sessionId", "generation", "positionUS"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[struct{}, mediaanalysis.Artifact](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_analysis_artifacts_artifact", Method: "GET", Path: "/v1/items/{id}/analysis/artifacts/{artifact}", Summary: "Read title analysis artifacts", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"sourceId", "sourceRevision", "mappingRevision", "sessionId", "generation", "positionUS"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[struct {
		mediaanalysis.Target
		Action string `json:"action"`
	}, ingestion.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_analysis_jobs", Method: "POST", Path: "/v1/items/{id}/analysis/jobs", Summary: "Run title analysis jobs", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[struct {
		mediaanalysis.Target
		Action string `json:"action"`
	}, AnalysisControlResponse](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_analysis_jobs_job_control", Method: "POST", Path: "/v1/items/{id}/analysis/jobs/{job}/control", Summary: "Run title analysis jobs control", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, SuccessStatuses: []int{202}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[mediaanalysis.Mutation, mediaanalysis.MutationResult](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_analysis_markers", Method: "POST", Path: "/v1/items/{id}/analysis/markers", Summary: "Run title analysis markers", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/media_analysis.go:108
	if err := apikit.RegisterHTTP[struct {
		mediaanalysis.Target
		Tier             string                    `json:"tier"`
		Operations       []string                  `json:"operations"`
		Trickplay        catalog.TrickplaySettings `json:"trickplay"`
		ExpectedRevision int64                     `json:"expectedRevision"`
	}, catalog.ScanPolicy](registry, apikit.Metadata{Audience: apikit.Clients, ID: "put_items_id_analysis_policy", Method: "PUT", Path: "/v1/items/{id}/analysis/policy", Summary: "Save title analysis policy", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2097152, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playback_chapters.go:12
	if err := apikit.RegisterHTTP[struct{}, playback.ChapterProjection](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_playback_sessions_id_chapters", Method: "GET", Path: "/v1/playback/sessions/{id}/chapters", Summary: "Read playback sessions chapters", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Query: []string{"cursor", "limit", "revision"}, QueryIntegers: limitQuery(100, 100), Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playback_diagnostics.go:15
	if err := apikit.RegisterHTTP[struct{}, struct {
		Scope       map[string]string `json:"scope"`
		ObservedAt  string            `json:"observedAt"`
		FreshUntil  string            `json:"freshUntil"`
		Diagnostics playbackOverview  `json:"diagnostics"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_admin_playback_diagnostics", Method: "GET", Path: "/v1/admin/playback/diagnostics", Summary: "Read server playback diagnostics", Access: apikit.Owner, Requires: apikit.ScopeAdmin, Lane: apikit.Media, Admission: lanePlayback, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/playback_offers.go:12
	if err := apikit.RegisterHTTP[struct{}, playback.Offers](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_playback_offers", Method: "GET", Path: "/v1/items/{id}/playback-offers", Summary: "Read title playback offers", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: lanePlayback, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"revision", "sessionId"}, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/prepared_media.go:88
	if err := apikit.RegisterHTTP[struct{}, preparedmedia.View](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_prepared_versions", Method: "GET", Path: "/v1/items/{id}/prepared-versions", Summary: "Read title prepared versions", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/prepared_media.go:91
	if err := apikit.RegisterHTTP[preparedmedia.Request, preparedmedia.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_items_id_optimizations", Method: "POST", Path: "/v1/items/{id}/optimizations", Summary: "Run title optimizations", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/prepared_media.go:110
	if err := apikit.RegisterHTTP[preparedmedia.Command, preparedmedia.Version](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_prepared_versions_id_delete", Method: "POST", Path: "/v1/prepared-versions/{id}/delete", Summary: "Run prepared versions delete", Access: apikit.Device, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:844
	if err := apikit.RegisterHTTP[struct{}, struct {
		Providers []map[string]string `json:"providers"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_attribution", Method: "GET", Path: "/v1/attribution", Summary: "Read attribution", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:856
	if err := apikit.RegisterHTTP[struct{}, SystemInfo](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_system", Method: "GET", Path: "/v1/system", Summary: "Read system", Access: apikit.Public, Requires: apikit.ScopeOpen, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:859
	if err := apikit.RegisterHTTP[struct{}, struct {
		Viewer identity.Viewer `json:"viewer"`
	}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_me", Method: "GET", Path: "/v1/me", Summary: "Read the current viewer", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:867
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "delete_sessions_current", Method: "DELETE", Path: "/v1/sessions/current", Summary: "Sign out this session family", Access: apikit.Device, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneSecurityFence, Cost: apikit.Constant, BodyLimit: 0, Status: 204, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:868
	if err := apikit.RegisterHTTP[hosted.Signed, struct{}](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_hosted_wake", NoResponseBody: true, Method: "POST", Path: "/v1/hosted/wake", Summary: "Run hosted wake", Access: apikit.Public, Requires: apikit.ScopeSession, Lane: apikit.Security, Admission: laneAuth, Cost: apikit.Constant, BodyLimit: 65536, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:1018
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Integrations, ID: "get_items_id_art_kind", Method: "GET", Path: "/v1/items/{id}/art/{kind}", Summary: "Read title art", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Query: []string{"ld", "r", "sd", "size", "v", "w"}, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:1053
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_grant_file", Method: "GET", Path: "/v1/media/{grant}/{file}", Summary: "Read media", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "storage_offline", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/router.go:1113
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_media_grant", Method: "GET", Path: "/v1/media/{grant}", Summary: "Read media", Access: apikit.Device, Requires: apikit.ScopePlayback, Lane: apikit.Media, Admission: laneMediaBody, Cost: apikit.Constant, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "storage_offline", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/trickplay.go:24
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_trickplay_set_thumbnails_vtt", Method: "GET", Path: "/v1/items/{id}/trickplay/{set}/thumbnails.vtt", Summary: "Read title trickplay thumbnails.vtt", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Media, Admission: laneMedia, Cost: apikit.PageSized, BodyLimit: 0, Status: 200, ResponseContentType: "application/octet-stream", Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/trickplay.go:61
	if err := apikit.RegisterHTTP[struct{}, mediaanalysis.TrickplayView](registry, apikit.Metadata{Audience: apikit.Clients, ID: "get_items_id_trickplay", Method: "GET", Path: "/v1/items/{id}/trickplay", Summary: "Read title trickplay", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 0, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[preparedmedia.Command, preparedmedia.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_optimization_jobs_id_cancel", Method: "POST", Path: "/v1/optimization-jobs/{id}/cancel", Summary: "Run optimization jobs cancel", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[preparedmedia.Command, preparedmedia.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_optimization_jobs_id_retry", Method: "POST", Path: "/v1/optimization-jobs/{id}/retry", Summary: "Run optimization jobs retry", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Default, Admission: laneDefault, Cost: apikit.Constant, BodyLimit: 65536, Status: 200, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	// internal/httpapi/activity.go:36
	if err := apikit.RegisterHTTP[struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}, operations.Job](registry, apikit.Metadata{Audience: apikit.Clients, ID: "post_libraries_id_lyrics_fetch", Method: "POST", Path: "/v1/libraries/{id}/lyrics/fetch", Summary: "Run library lyrics fetch", Access: apikit.Owner, Requires: apikit.ScopeLibrary, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.Constant, BodyLimit: 2 << 20, Status: 202, Errors: []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}}); err != nil {
		return err
	}
	return nil
}
