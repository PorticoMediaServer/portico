package httpapi

import "github.com/PorticoMediaServer/portico/apikit"

// Non-API infrastructure has no credential authority. Every API operation
// declares its credential scope on the typed registry.
var routeScopes = map[string]apikit.Scope{
	"GET ":                apikit.ScopeOpen,
	"POST ":               apikit.ScopeOpen,
	"PUT ":                apikit.ScopeOpen,
	"/":                   apikit.ScopeOpen,
	"/dlna/":              apikit.ScopeOpen,
	"/dlna/media/":        apikit.ScopeOpen,
	"GET /receiver/cast/": apikit.ScopeOpen,
}
