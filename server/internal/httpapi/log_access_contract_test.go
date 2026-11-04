package httpapi

import (
	"net/http"
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
		{"POST /v1/admin/logs/debug-window", apikit.Owner},
		{"PATCH /v1/admin/logs/settings", apikit.Owner},
	} {
		metadata, ok := registry.MetadataForPattern(tc.pattern)
		if !ok || metadata.Access != tc.access || metadata.Requires != apikit.ScopeAdmin || tc.access == apikit.Admin && metadata.AdminScope != "logs" {
			t.Errorf("%s must publish access=%s and admin scope, got %+v (registered=%v)", tc.pattern, tc.access, metadata, ok)
		}
	}
}

func TestAdministratorCannotOpenALogDebugWindow(t *testing.T) {
	f := administrationFixtureNew(t)
	raw := http.NewServeMux()
	f.deps.diagnosticsRoutes(raw)
	for name, handler := range map[string]http.Handler{"registered route": f.handler, "handler without mutation gate": raw} {
		w := notificationRequest(handler, "POST", f.admin, "/v1/admin/logs/debug-window", `{"minutes":5,"operationId":"admin-debug-window"}`)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s admitted an administrator debug window: %d %s", name, w.Code, w.Body)
		}
	}
}
