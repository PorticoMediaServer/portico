package catalog

import "github.com/PorticoMediaServer/portico/server/internal/compactcatalog"

// Requests share the readiness check and their existing read snapshot.
func (s *Service) compactProjectionReady(domains ...int) error {
	return compactcatalog.CheckReadiness(s.Context(), s.read(), domains...)
}
