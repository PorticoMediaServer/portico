package api

import (
	"net/http"
	"slices"
	"strings"
)

// A note returned to a server page works only from that HTTPS page, at an address the
// server serves. Hosted-web/native notes without an audience keep their existing path.
// Forwarded scheme and host are believed only from the configured trusted proxies.
func (s *Server) porticoAudienceAllowed(r *http.Request, audience string) bool {
	if audience == "" {
		return true
	}
	origin, ok := webOrigin(audience)
	if !ok || !strings.HasPrefix(origin, "https://") || strings.TrimSuffix(audience, ":443") != origin || r.Header.Get("Origin") != origin || !s.secure(r) {
		return false
	}
	address, ok := hostPort(strings.TrimPrefix(origin, "https://"), 443)
	if !ok || s.requestAddress(r, true) != address {
		return false
	}
	settings, allowed := s.access.current()
	return slices.Contains(allowed, address) || s.underOwnZone(address, settings)
}
