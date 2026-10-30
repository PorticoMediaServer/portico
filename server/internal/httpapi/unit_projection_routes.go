package httpapi

import (
	"net/http"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"

	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"strings"
)

type projectedScope struct {
	ServerID    string `json:"serverId"`
	ViewerFence string `json:"viewerFence"`
}
type projectedConsoleEnvelope[T any] struct {
	Scope projectedScope `json:"scope"`
	Data  T              `json:"data"`
}
type projectedListeningScope struct {
	ServerID  string `json:"serverId"`
	Authority string `json:"authority"`
	AccountID string `json:"accountId"`
	ProfileID string `json:"profileId"`
}
type projectedViewerEnvelope[T any] struct {
	Scope projectedListeningScope `json:"scope"`
	Data  T                       `json:"data"`
}
type projectedItems struct {
	Items      []catalog.Item `json:"items"`
	NextCursor string         `json:"nextCursor"`
}

var projectedPatterns = map[string]bool{
	"GET /v1/libraries/{id}/content":              true,
	"GET /v1/libraries/{id}/browse":               true,
	"GET /v1/libraries/{id}/discover":             true,
	"GET /v1/collections/{id}/items":              true,
	"GET /v1/items":                               true,
	"GET /v1/items/{id}":                          true,
	"GET /v1/items/{id}/detail":                   true,
	"PUT /v1/items/{id}/personal-state":           true,
	"PUT /v1/items/personal-state:batch":          true,
	"GET /v1/saved-resources/{id}/content":        true,
	"GET /v1/admin/dashboard/report":              true,
	"GET /v1/admin/play-history":                  true,
	"GET /v1/admin/play-history/summary":          true,
	"GET /v1/libraries/{id}/listening/selection":  true,
	"GET /v1/people/{id}":                         true,
	"GET /v1/notifications/inbox":                 true,
	"GET /v1/notifications/unread-count":          true,
	"POST /v1/feedback/reports":                   true,
	"POST /v1/admin/feedback/reports/{id}/status": true,
	"GET /v1/feedback/reports":                    true,
	"GET /v1/feedback/reports/{id}":               true,
	"GET /v1/admin/feedback/reports":              true,
	"GET /v1/admin/feedback/reports/{id}":         true,
	"GET /v1/notifications":                       true,
	"GET /v1/admin/console/jobs":                  true,
	"POST /v1/admin/console/jobs":                 true,
	"POST /v1/admin/console/jobs/{id}/{action}":   true,
	"GET /v1/admin/console/schedules":             true,
	"PUT /v1/admin/console/schedules":             true,
	"GET /v1/admin/console/panels/{panel}":        true,
	"GET /v1/admin/console/diagnostics/{lane}":    true,
	"POST /v1/admin/console/diagnostics/capture":  true,
	"POST /v1/admin/console/exports":              true,
	"GET /v1/admin/console/exports/{id}":          true,
	"GET /v1/admin/playback/history":              true,
	"GET /v1/admin/console/alerts":                true,
}

func (d Dependencies) projectedHandle(mux *http.ServeMux, pattern string, handler http.HandlerFunc) {
	if d.registry != nil && projectedPatterns[pattern] {
		return
	}
	if d.registry != nil {
		if metadata, ok := d.registry.MetadataForPattern(pattern); ok && metadata.HTTP {
			d.httpHandle(mux, pattern, handler)
			return
		}
	}
	mux.HandleFunc(pattern, handler)
}
func addProjection[Request, Response any](registry *apikit.Registry, mux *http.ServeMux, pattern string, audience apikit.Audience, scope apikit.Scope, admission string, query []string) error {
	method, path, _ := strings.Cut(pattern, " ")
	lane := apikit.Default
	if admission == laneBrowsing {
		lane = apikit.Browsing
	}
	access := apikit.Device
	if strings.HasPrefix(path, "/v1/admin/play-history") || path == "/v1/admin/dashboard/report" {
		access = apikit.Owner
	}
	bodyLimit := int64(0)
	if method != http.MethodGet && method != http.MethodHead {
		bodyLimit = 1048576
	}
	var integers []apikit.QueryInteger
	if method == http.MethodGet || method == http.MethodHead {
		integers = projectionLimitQueries(path)
	}
	id := strings.ToLower(method) + "_" + strings.NewReplacer("/", "_", "{", "", "}", "", "-", "_", ":", "_").Replace(strings.TrimPrefix(path, "/v1/"))
	var success, statuses []int
	var responseHeaders []apikit.ResponseHeader
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}
	if method == http.MethodGet && (path == "/v1/items/{id}" || path == "/v1/items/{id}/detail") {
		success = []int{202}
		statuses = []int{304}
		responseHeaders = fileInfoWindowHeaders()
		failures = append(failures, "stale_continuation", "metadata_unavailable")
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[Request, Response]{Metadata: apikit.Metadata{Audience: audience, Admission: admission, ID: id, Method: method, Path: path, Summary: "Read or update the selected projection", Access: access, Requires: scope, Lane: lane, Cost: apikit.PageSized, Status: 200, SuccessStatuses: success, ResponseStatuses: statuses, ResponseHeaders: responseHeaders, BodyLimit: bodyLimit, Query: query, QueryIntegers: integers, Errors: failures}, Serve: func(w http.ResponseWriter, r *http.Request) {
		// A projection whose handler this server did not mount (no scheduler,
		// no console) is an unknown route and answers like one.
		if _, mounted := mux.Handler(r); mounted == "" {
			write(w, http.StatusNotFound, map[string]any{"error": map[string]any{"code": "not_found", "message": "The request could not be completed.", "retryable": false}})
			return
		}
		mux.ServeHTTP(w, r)
	}})
}
func registerUnitProjections(registry *apikit.Registry, d Dependencies) error {
	private := d
	private.registry = nil
	private.httpContractsPrivate = true
	mux := http.NewServeMux()
	private.discoveryRoutes(mux)
	private.playHistoryRoutes(mux)
	private.dashboardReportRoute(mux, &telemetryService{d: private})
	private.peopleRoutes(mux)
	private.listeningRoutes(mux)
	private.consoleRoutes(mux)
	private.notificationRoutes(mux)
	mux.HandleFunc("GET /v1/items", private.itemsReadHandler)
	mux.HandleFunc("GET /v1/items/{id}", private.itemReadHandler)
	if err := addProjection[noBody, catalog.ContentEnvelope](registry, mux, "GET /v1/libraries/{id}/content", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"view", "entityId", "sort", "direction", "category", "q", "cursor", "limit", "start", "section"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedItems](registry, mux, "GET /v1/libraries/{id}/browse", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"sort", "direction", "category", "q", "cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, catalog.Discovery](registry, mux, "GET /v1/libraries/{id}/discover", apikit.Clients, apikit.ScopeRead, laneBrowsing, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedItems](registry, mux, "GET /v1/collections/{id}/items", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"sort", "direction", "category", "q", "cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedItems](registry, mux, "GET /v1/items", apikit.Integrations, apikit.ScopeRead, laneBrowsing, []string{"libraryId", "cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, fileInfoItemResponse](registry, mux, "GET /v1/items/{id}", apikit.Integrations, apikit.ScopeRead, laneBrowsing, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, fileInfoDetailResponse](registry, mux, "GET /v1/items/{id}/detail", apikit.Integrations, apikit.ScopeRead, laneBrowsing, []string{"related"}); err != nil {
		return err
	}
	if err := addProjection[catalog.PersonalMutation, catalog.PersonalReceipt](registry, mux, "PUT /v1/items/{id}/personal-state", apikit.Clients, apikit.ScopePlayback, laneBrowsing, nil); err != nil {
		return err
	}
	if err := addProjection[catalog.PersonalBatchMutation, catalog.PersonalBatchReceipt](registry, mux, "PUT /v1/items/personal-state:batch", apikit.Clients, apikit.ScopePlayback, laneBrowsing, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, catalog.SavedResourceContent](registry, mux, "GET /v1/saved-resources/{id}/content", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"cursor", "limit", "sort", "direction", "filter"}); err != nil {
		return err
	}
	if err := addProjection[noBody, DashboardReport](registry, mux, "GET /v1/admin/dashboard/report", apikit.Clients, apikit.ScopeAdmin, laneAdmin, dashboardReportQuery); err != nil {
		return err
	}
	if err := addProjection[noBody, catalog.PlayHistoryPage](registry, mux, "GET /v1/admin/play-history", apikit.Clients, apikit.ScopeAdmin, laneAdmin, []string{"accountId", "libraryId", "period", "cursor", "limit", "media", "q", "sort", "direction", "timeZone", "until"}); err != nil {
		return err
	}
	if err := addProjection[noBody, catalog.PlaySummary](registry, mux, "GET /v1/admin/play-history/summary", apikit.Clients, apikit.ScopeAdmin, laneAdmin, []string{"period", "timeZone", "until", "accountId", "libraryId", "media", "q"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedViewerEnvelope[catalog.ListeningSelection]](registry, mux, "GET /v1/libraries/{id}/listening/selection", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"kind", "entityId", "cursor", "limit", "mode", "seed", "resume", "startItem"}); err != nil {
		return err
	}
	if err := addProjection[noBody, catalog.PersonPage](registry, mux, "GET /v1/people/{id}", apikit.Clients, apikit.ScopeRead, laneBrowsing, []string{"role", "cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.NoticeInbox]](registry, mux, "GET /v1/notifications/inbox", apikit.Clients, apikit.ScopeRead, laneDefault, []string{"audience", "state", "cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.NoticeUnread]](registry, mux, "GET /v1/notifications/unread-count", apikit.Clients, apikit.ScopeRead, laneDefault, []string{"audience"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.FeedbackList]](registry, mux, "GET /v1/feedback/reports", apikit.Clients, apikit.ScopeRead, laneDefault, []string{"cursor", "limit", "status", "kind", "category", "itemId", "reporter"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.FeedbackReport]](registry, mux, "GET /v1/feedback/reports/{id}", apikit.Clients, apikit.ScopeRead, laneDefault, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.FeedbackList]](registry, mux, "GET /v1/admin/feedback/reports", apikit.Clients, apikit.ScopeAdmin, laneAdmin, []string{"cursor", "limit", "status", "kind", "category", "itemId", "reporter"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.FeedbackReport]](registry, mux, "GET /v1/admin/feedback/reports/{id}", apikit.Clients, apikit.ScopeAdmin, laneAdmin, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Inbox]](registry, mux, "GET /v1/notifications", apikit.Clients, apikit.ScopeRead, laneDefault, []string{"cursor", "limit"}); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Page[operations.Job]]](registry, mux, "GET /v1/admin/console/jobs", apikit.Integrations, apikit.ScopeAdmin, laneAdmin, []string{"cursor"}); err != nil {
		return err
	}
	if err := addProjection[operations.RunJob, projectedConsoleEnvelope[operations.Job]](registry, mux, "POST /v1/admin/console/jobs", apikit.Integrations, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	if err := addProjection[operations.JobCommand, projectedConsoleEnvelope[operations.Job]](registry, mux, "POST /v1/admin/console/jobs/{id}/{action}", apikit.Integrations, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[[]operations.Schedule]](registry, mux, "GET /v1/admin/console/schedules", apikit.Clients, apikit.ScopeAdmin, laneAdmin, nil); err != nil {
		return err
	}
	if err := addProjection[operations.ScheduleChange, projectedConsoleEnvelope[operations.Schedule]](registry, mux, "PUT /v1/admin/console/schedules", apikit.Clients, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Measurement]](registry, mux, "GET /v1/admin/console/panels/{panel}", apikit.Clients, apikit.ScopeAdmin, laneAdmin, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Page[operations.Record]]](registry, mux, "GET /v1/admin/console/diagnostics/{lane}", apikit.Clients, apikit.ScopeAdmin, laneAdmin, []string{"cursor"}); err != nil {
		return err
	}
	if err := addProjection[operations.Capture, projectedConsoleEnvelope[operations.Capture]](registry, mux, "POST /v1/admin/console/diagnostics/capture", apikit.Clients, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	if err := addProjection[operations.ExportRequest, projectedConsoleEnvelope[operations.Export]](registry, mux, "POST /v1/admin/console/exports", apikit.Clients, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Export]](registry, mux, "GET /v1/admin/console/exports/{id}", apikit.Clients, apikit.ScopeAdmin, laneAdmin, nil); err != nil {
		return err
	}
	if err := addProjection[noBody, projectedConsoleEnvelope[[]operations.Alert]](registry, mux, "GET /v1/admin/console/alerts", apikit.Clients, apikit.ScopeAdmin, laneAdmin, nil); err != nil {
		return err
	}
	if err := addProjection[operations.FeedbackSubmission, projectedConsoleEnvelope[operations.FeedbackSubmissionResult]](registry, mux, "POST /v1/feedback/reports", apikit.Clients, apikit.ScopePlayback, laneDefault, nil); err != nil {
		return err
	}
	if err := addProjection[operations.FeedbackTransition, projectedConsoleEnvelope[operations.FeedbackReport]](registry, mux, "POST /v1/admin/feedback/reports/{id}/status", apikit.Clients, apikit.ScopeAdmin, laneAdminHeavy, nil); err != nil {
		return err
	}
	historyService := &telemetryService{d: private}
	historyService.register(mux, "GET /v1/admin/playback/history", []string{"period", "limit", "cursor"}, func(r *http.Request, a operations.Authorize) (any, error) {
		period, limit, err := historyQuery(r)
		if err != nil {
			return nil, err
		}
		return private.Console.PlaybackHistory(r.Context(), a, period, r.URL.Query().Get("cursor"), limit)
	})
	if err := addProjection[noBody, projectedConsoleEnvelope[operations.Page[operations.PlaybackHistoryEntry]]](registry, mux, "GET /v1/admin/playback/history", apikit.Clients, apikit.ScopeAdmin, laneAdmin, []string{"period", "limit", "cursor"}); err != nil {
		return err
	}
	return nil
}
