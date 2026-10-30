package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/reading"
)

func registerReadingHTTPContracts(registry *apikit.Registry) error {
	if err := registerReadingSavedGroupHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerReadingFavoriteHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerReadingSavedHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerReadingMarkHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerReadingAnnotationHTTPContracts(registry); err != nil {
		return err
	}
	if err := registerReadingPeriodicalGroupsHTTPContract(registry); err != nil {
		return err
	}
	if err := registerReadingPeriodicalIssuesHTTPContract(registry); err != nil {
		return err
	}
	if err := registerReadingDiscoverHTTPContract(registry); err != nil {
		return err
	}
	if err := registerReadingSeriesHTTPContract(registry); err != nil {
		return err
	}
	errors := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "revision_required", "revision_mismatch", "request_in_progress", "body_too_large", "internal"}
	metadata := func(id, method, path, summary string, scope apikit.Scope) apikit.Metadata {
		return apikit.Metadata{Audience: apikit.Clients, ID: id, Method: method, Path: path, Summary: summary, Access: apikit.Device, Requires: scope, Lane: apikit.Browsing, Admission: laneBrowsing, Cost: apikit.PageSized, Status: 200, Errors: errors}
	}
	m := metadata("get_reading_items", "GET", "/v1/reading/items", "List visible reading publications", apikit.ScopeRead)
	m.Query = []string{"libraryId", "cursor", "limit"}
	m.QueryIntegers = emptyLimitQuery(50, 100)
	if err := apikit.RegisterHTTP[struct{}, ReadingItemsResponse](registry, m); err != nil {
		return err
	}
	m = metadata("get_reading_continue", "GET", "/v1/reading/continue", "List the viewer's unfinished reading publications", apikit.ScopeRead)
	m.Query = []string{"libraryId", "cursor", "limit"}
	m.QueryIntegers = emptyLimitQuery(50, 100)
	if err := apikit.RegisterHTTP[struct{}, ReadingContinueResponse](registry, m); err != nil {
		return err
	}
	m = metadata("get_items_itemId_reading_versions", "GET", "/v1/items/{itemId}/reading/versions", "List a reading publication's actual versions", apikit.ScopeRead)
	if err := apikit.RegisterHTTP[struct{}, ReadingVersionsResponse](registry, m); err != nil {
		return err
	}
	m = metadata("post_items_itemId_reading_reread", "POST", "/v1/items/{itemId}/reading/reread", "Re-read the exact reading source and refresh file metadata", apikit.ScopePlayback)
	m.Access = apikit.Owner
	m.Lane, m.Admission, m.Cost = apikit.Media, laneMediaBody, apikit.Constant
	m.BodyLimit = 4096
	m.Errors = append(m.Errors, "metadata_conflict", "source_unavailable")
	if err := apikit.RegisterHTTP[reading.RereadRequest, reading.RereadResult](registry, m); err != nil {
		return err
	}
	m = metadata("post_reading_sessions", "POST", "/v1/reading/sessions", "Open an authorized reading publication", apikit.ScopePlayback)
	// Preparation inspects bounded source bytes, including paced Remote reads.
	m.Lane, m.Admission = apikit.Media, laneMediaBody
	m.BodyLimit = 4096
	if err := apikit.RegisterHTTP[ReadingSessionRequest, ReadingSessionResponse](registry, m); err != nil {
		return err
	}
	m = metadata("post_reading_sessions_sessionId_file_save", "POST", "/v1/reading/sessions/{sessionId}/file-save", "Save the selected original file through its live authenticated Reader session", apikit.ScopePlayback)
	m.Lane, m.Admission, m.Cost = apikit.Media, laneMediaBody, apikit.Constant
	m.Headers, m.BodyLimit, m.ResponseContentType = []string{"If-Match"}, 4096, "application/octet-stream"
	if err := apikit.RegisterHTTP[ReadingFileSaveRequest, SubtitleBytesContract](registry, m); err != nil {
		return err
	}
	m = metadata("get_reading_sessions_sessionId_progress", "GET", "/v1/reading/sessions/{sessionId}/progress", "Read the selected publication's saved location", apikit.ScopeRead)
	m.Headers = []string{"If-Match"}
	if err := apikit.RegisterHTTP[struct{}, ReadingProgressResponse](registry, m); err != nil {
		return err
	}
	m = metadata("get_reading_search", "GET", "/v1/reading/search", "Search the persisted Reading index across the viewer's ready libraries", apikit.ScopeRead)
	m.Query = []string{"q", "cursor", "limit", "libraryId"}
	m.QueryIntegers = emptyLimitQuery(20, 50)
	m.Errors = append(append([]string{}, m.Errors...), "search_busy")
	if err := apikit.RegisterHTTP[struct{}, ReadingIndexedSearchResponse](registry, m); err != nil {
		return err
	}
	m = metadata("get_reading_sessions_sessionId_search", "GET", "/v1/reading/sessions/{sessionId}/search", "Search the selected publication's persisted text index", apikit.ScopeRead)
	m.Query = []string{"q", "cursor", "limit"}
	m.QueryIntegers = emptyLimitQuery(20, 50)
	m.Errors = append(append([]string{}, m.Errors...), "search_busy")
	if err := apikit.RegisterHTTP[struct{}, ReadingSearchResponse](registry, m); err != nil {
		return err
	}
	m = metadata("put_reading_sessions_sessionId_progress", "PUT", "/v1/reading/sessions/{sessionId}/progress", "Save the selected publication's location with compare and swap", apikit.ScopePlayback)
	m.Headers = []string{"If-Match"}
	// Validating a saved location can inspect the actual publication bytes.
	m.Lane, m.Admission = apikit.Media, laneMediaBody
	m.BodyLimit = 8192
	if err := apikit.RegisterHTTP[ReadingProgressRequest, ReadingProgressSavedResponse](registry, m); err != nil {
		return err
	}
	m = metadata("post_reading_offline_progress", "POST", "/v1/reading/offline/progress", "Reconcile an observed location from this viewer's signed offline publication", apikit.ScopePlayback)
	m.Lane, m.Admission = apikit.Media, laneMediaBody
	m.BodyLimit = 32768
	if err := apikit.RegisterHTTP[ReadingOfflineProgressRequest, ReadingOfflineProgressResponse](registry, m); err != nil {
		return err
	}
	m = metadata("post_reading_offline_progress_read", "POST", "/v1/reading/offline/progress/read", "Read this viewer's saved positions for signed offline publications", apikit.ScopePlayback)
	m.BodyLimit = 512 << 10
	if err := apikit.RegisterHTTP[ReadingOfflineProgressReadRequest, ReadingOfflineProgressReadResponse](registry, m); err != nil {
		return err
	}
	m = metadata("delete_reading_sessions_sessionId", "DELETE", "/v1/reading/sessions/{sessionId}", "Close this caller's reading session", apikit.ScopePlayback)
	m.Status, m.NoResponseBody = 204, true
	if err := apikit.RegisterHTTP[struct{}, struct{}](registry, m); err != nil {
		return err
	}
	for _, method := range []string{"GET", "HEAD"} {
		id, status := "get_reading_sessions_sessionId_source", 206
		if method == "HEAD" {
			id, status = "head_reading_sessions_sessionId_source", 200
		}
		m = metadata(id, method, "/v1/reading/sessions/{sessionId}/source", "Read the selected PDF's exact byte range", apikit.ScopePlayback)
		m.Lane, m.Admission, m.Cost = apikit.Media, laneMediaBody, apikit.Constant
		m.Headers, m.Status, m.ResponseContentType = []string{"If-Match", "Range"}, status, "application/pdf"
		m.ResponseStatuses = []int{416}
		if method == "GET" {
			m.ResponseStatuses = []int{200, 416}
		}
		if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, m); err != nil {
			return err
		}
	}
	m = metadata("get_reading_sessions_sessionId_assets", "GET", "/v1/reading/sessions/{sessionId}/assets", "Read one enumerated original EPUB asset", apikit.ScopePlayback)
	m.Lane, m.Admission, m.Cost = apikit.Media, laneMediaBody, apikit.Constant
	m.Query, m.Headers, m.ResponseContentType = []string{"path"}, []string{"If-Match", "If-None-Match"}, "application/octet-stream"
	m.ResponseStatuses = []int{304}
	if err := apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, m); err != nil {
		return err
	}
	m = metadata("get_reading_sessions_sessionId_pages_pageIndex", "GET", "/v1/reading/sessions/{sessionId}/pages/{pageIndex}", "Read one bounded comic page image", apikit.ScopePlayback)
	m.Lane, m.Admission, m.Cost = apikit.Media, laneMediaBody, apikit.Constant
	m.Query, m.Headers, m.ResponseContentType = []string{"width"}, []string{"If-Match", "If-None-Match"}, "application/octet-stream"
	m.ResponseStatuses = []int{304}
	return apikit.RegisterHTTP[struct{}, SubtitleBytesContract](registry, m)
}
