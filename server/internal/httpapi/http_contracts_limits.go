package httpapi

import (
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

// These describe the existing parsers; they do not parse or enforce queries.
func limitQuery(defaultLimit, maximum int) []apikit.QueryInteger {
	return []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: defaultLimit, Maximum: maximum}}
}

func emptyLimitQuery(defaultLimit, maximum int) []apikit.QueryInteger {
	return []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: defaultLimit, Maximum: maximum, EmptyIsDefault: true}}
}

// Legacy catalogue handlers discard Atoi's error. Syntax errors become zero;
// range errors saturate to the host's signed integer bound before pageLimit.
var legacyBrowseLimitQuery = []apikit.QueryInteger{{Name: "limit", Minimum: 1, Default: catalog.BrowseDefaultLimit, Maximum: catalog.BrowseMaximumLimit, Clamped: true, DefaultBelowMinimum: true, DefaultOnInvalid: true, EmptyIsDefault: true, Description: "Absent, empty, non-integer syntax and values below 1 use the default. Integer overflow saturates to the host integer bound before applying the default or upper clamp."}}

func projectionLimitQueries(path string) []apikit.QueryInteger {
	switch path {
	case "/v1/libraries/{id}/content", "/v1/libraries/{id}/browse", "/v1/collections/{id}/items", "/v1/items", "/v1/libraries/{id}/listening/selection":
		return legacyBrowseLimitQuery
	case "/v1/saved-resources/{id}/content", "/v1/people/{id}":
		return limitQuery(40, 100)
	case "/v1/admin/play-history", "/v1/admin/playback/history":
		return emptyLimitQuery(50, 200)
	case "/v1/notifications/inbox", "/v1/feedback/reports", "/v1/admin/feedback/reports":
		return emptyLimitQuery(40, 100)
	case "/v1/notifications":
		return emptyLimitQuery(40, 40)
	}
	return nil
}
