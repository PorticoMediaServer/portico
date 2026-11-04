package httpapi

import (
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

func TestLogRouteContractsPublishTheirActualTier(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pattern string
		access  apikit.Access
	}{
		{"GET /v1/admin/logs", apikit.Admin},
		{"GET /v1/admin/logs/events", apikit.Admin},
		{"GET /v1/admin/logs/settings", apikit.Admin},
		{"POST /v1/admin/logs/debug-window", apikit.Admin},
		{"PATCH /v1/admin/logs/settings", apikit.Owner},
	} {
		metadata, ok := registry.MetadataForPattern(tc.pattern)
		if !ok || metadata.Access != tc.access || metadata.Requires != apikit.ScopeAdmin || tc.access == apikit.Admin && metadata.AdminScope != "logs" {
			t.Errorf("%s must publish access=%s and admin scope, got %+v (registered=%v)", tc.pattern, tc.access, metadata, ok)
		}
	}
}
