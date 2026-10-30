package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// Typed read contracts retain the handlers' conditional responses and scoped
// snapshot composition. The first-party screens and outside clients share them.
func registerPersonalReads(r *apikit.Registry, d Dependencies) error {
	errors := []string{"invalid_request", "invalid_cursor", "stale_continuation", "unauthorized", "not_permitted", "not_found", "rate_limited", "internal"}
	if err := apikit.RegisterRaw(r, apikit.RawRoute[noBody, catalog.HomeDocument]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_home", Method: "GET", Path: "/v1/home", Summary: "Read this viewer's composed Home rows and destinations", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"limit"}, QueryIntegers: []apikit.QueryInteger{homeLimitQuery}, Errors: errors}, Serve: d.serveHomeDocument}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(r, apikit.RawRoute[noBody, catalog.HomeRow]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_home_row", Method: "GET", Path: "/v1/home/rows/{id}", Summary: "Page a composed Home row", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"limit", "cursor", "start", "revision", "anchorId", "group", "sourceId"}, QueryIntegers: []apikit.QueryInteger{homeLimitQuery}, Errors: errors}, Serve: d.serveHomeRow}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(r, apikit.RawRoute[noBody, catalog.SavedContentEnvelope]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_saved_content", Method: "GET", Path: "/v1/content", Summary: "Read a saved list or playlist directory", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, Query: []string{"view", "sort", "direction", "filter", "shape", "limit", "cursor"}, QueryIntegers: limitQuery(40, 100), Errors: errors}, Serve: d.serveGlobalSavedContent}); err != nil {
		return err
	}
	return apikit.RegisterRaw(r, apikit.RawRoute[noBody, catalog.HistoryPage]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneDefault, ID: "get_personal_history", Method: "GET", Path: "/v1/personal-history", Summary: "Read this viewer's history within current title access", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Default, Cost: apikit.PageSized, Status: 200, Query: []string{"cursor", "period", "libraryId", "limit"}, QueryIntegers: limitQuery(40, 100), Errors: errors}, Serve: d.servePersonalHistory})
}
