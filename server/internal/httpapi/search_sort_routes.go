package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func registerSearchSortRoutes(registry *apikit.Registry, d Dependencies) error {
	failures := []string{"invalid_request", "unauthorized", "not_permitted", "not_found", "internal"}
	libraryFailures := append(append([]string{}, failures...), "revision_mismatch")
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, catalog.SearchEnvelope]{Metadata: apikit.Metadata{Audience: apikit.Integrations, Admission: laneExpensive, ID: "search", Method: "GET", Path: "/v1/search", Summary: "Search titles, people, years and collections", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200,
		Query: []string{"q", "group", "groups", "cursor", "limit", "sort", "direction", "libraryIds", "record"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 40, Maximum: 40, Clamped: true}}, Errors: failures,
	}, Serve: d.searchHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[browseRequestBody, readingBrowseQueryResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "browse_library", Method: "POST", Path: "/v1/libraries/{id}/browse", Summary: "Read a window of a library pivot", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: []apikit.ResponseHeader{{Status: 202, Name: "Location", Description: "Same-library Reading browse preparation", Required: true}, {Status: 202, Name: "Retry-After", Description: "Minimum polling delay in seconds", Required: true}}, BodyLimit: 65536, Errors: append(append([]string{}, libraryFailures...), "stale_continuation", "rate_limited", "search_busy")}, Serve: d.browseHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[browseRequestBody, catalog.BrowseResult]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "browse_across", Method: "POST", Path: "/v1/browse", Summary: "Read a window of a group of titles across the viewer's libraries", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, BodyLimit: 65536, Errors: failures}, Serve: d.browseAcrossHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, catalog.BrowseCapabilities]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_browse_capabilities", Method: "GET", Path: "/v1/browse-capabilities", Summary: "Read the vocabulary of lists across libraries", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 200,
		Query: []string{"pivot"}, Errors: failures,
	}, Serve: d.browseAcrossCapabilitiesHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, catalog.FacetPage]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_browse_facets", Method: "GET", Path: "/v1/browse/facets", Summary: "Read counted facet choices across the viewer's libraries, or within one list's titles", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200,
		Query: []string{"field", "q", "limit", "group", "value", "query", "pivot"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 200, Maximum: 200, Clamped: true}}, Errors: failures,
	}, Serve: d.browseAcrossFacetsHandler}); err != nil {
		return err
	}
	facetFailures := append(append([]string{}, libraryFailures...), "stale_continuation", "search_busy", "metadata_unavailable")
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, facetReadResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_library_facets", Method: "GET", Path: "/v1/libraries/{id}/facets", Summary: "Read counted facet choices", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: facetPreparingHeaders(),
		Query: []string{"field", "q", "limit", "offset", "query", "pivot", "after", "before", "at"}, QueryIntegers: []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: 200, Maximum: 200, Clamped: true}}, Errors: facetFailures,
	}, Serve: d.facetsHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, facetQueryResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_facet_query", Method: "GET", Path: "/v1/facet-queries/{id}", Summary: "Read a facet preparation or its ready window", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: facetPreparingHeaders(), Errors: facetFailures}, Serve: d.facetQueryHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "cancel_facet_query", Method: "DELETE", Path: "/v1/facet-queries/{id}", Summary: "Release a facet preparation handle", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 204, Errors: facetFailures}, Serve: d.facetQueryHandler}); err != nil {
		return err
	}
	if err := apikit.RegisterRaw(registry, apikit.RawRoute[noBody, genreReadResponse]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "get_genre_query", Method: "GET", Path: "/v1/genre-queries/{id}", Summary: "Read a genre preparation or its ready window", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.PageSized, Status: 200, SuccessStatuses: []int{202}, ResponseHeaders: genrePreparingHeaders(), Errors: facetFailures}, Serve: d.genreQueryHandler}); err != nil {
		return err
	}
	return apikit.RegisterRaw(registry, apikit.RawRoute[noBody, struct{}]{Metadata: apikit.Metadata{Audience: apikit.Clients, Admission: laneBrowsing, ID: "cancel_genre_query", Method: "DELETE", Path: "/v1/genre-queries/{id}", Summary: "Release a genre preparation handle", Access: apikit.Device, Requires: apikit.ScopeRead, Lane: apikit.Browsing, Cost: apikit.Constant, Status: 204, Errors: facetFailures}, Serve: d.genreQueryHandler})
}
