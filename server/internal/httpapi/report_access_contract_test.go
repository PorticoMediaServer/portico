package httpapi

import (
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

func TestTroubleshootingReportContractsPublishTheirActualTier(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	for _, pattern := range []string{
		"GET /v1/admin/feedback/reports",
		"GET /v1/admin/feedback/reports/{id}",
		"POST /v1/admin/feedback/reports/{id}/status",
		"GET /v1/admin/diagnostics/client-logs",
		"GET /v1/admin/diagnostics/client-logs/{id}",
		"GET /v1/admin/diagnostics/client-logs/{id}/download",
		"DELETE /v1/admin/diagnostics/client-logs/{id}",
	} {
		metadata, ok := registry.MetadataForPattern(pattern)
		if !ok || metadata.Access != apikit.Owner || metadata.Requires != apikit.ScopeAdmin || metadata.AdminScope != "" {
			t.Errorf("%s must publish owner with admin credentials: %+v (registered=%v)", pattern, metadata, ok)
		}
	}
	const stuckFiles = "GET /v1/admin/catalogue/failures"
	metadata, ok := registry.MetadataForPattern(stuckFiles)
	if !ok || metadata.Access != apikit.Admin || metadata.AdminScope != "library" || metadata.Requires != apikit.ScopeAdmin {
		t.Errorf("stuck files must publish admin_scope library: %+v (registered=%v)", metadata, ok)
	}
	for _, pattern := range []string{"GET /v1/feedback/reports", "GET /v1/feedback/reports/{id}", "POST /v1/feedback/reports"} {
		metadata, ok := registry.MetadataForPattern(pattern)
		if !ok || metadata.Access != apikit.Device {
			t.Errorf("reporters must retain their own device routes: %+v (registered=%v)", metadata, ok)
		}
	}
}

func TestTroubleshootingReportsAdmitOnlyTheirPublishedTier(t *testing.T) {
	f := administrationFixtureNew(t)
	if _, err := f.deps.DB.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(18,999,'Unreadable title',1760000000123)`); err != nil {
		t.Fatal(err)
	}
	for _, caller := range []struct {
		name, token string
		status      int
	}{{"owner", f.owner, 200}, {"administrator", f.admin, 200}, {"member", f.member, 403}} {
		t.Run("stuck files/"+caller.name, func(t *testing.T) {
			w := f.call("GET", "/v1/admin/catalogue/failures", caller.token, "")
			if w.Code != caller.status || caller.status == 200 && !strings.Contains(w.Body.String(), "Unreadable title") {
				t.Errorf("stuck files: %d %s", w.Code, w.Body)
			}
		})
	}
	// The owner check belongs to the Feedback handler too, independently of
	// contract-driven mutation admission.
	raw := http.NewServeMux()
	f.deps.notificationRoutes(raw)
	for name, handler := range map[string]http.Handler{"registered": f.handler, "unwrapped Feedback handler": raw} {
		for _, route := range []struct{ method, path, body string }{
			{"GET", "/v1/admin/feedback/reports", ""},
			{"GET", "/v1/admin/feedback/reports/unknown", ""},
			{"POST", "/v1/admin/feedback/reports/unknown/status", `{}`},
		} {
			for _, token := range []string{f.admin, f.member} {
				w := notificationRequest(handler, route.method, token, route.path, route.body)
				if w.Code != 403 {
					t.Errorf("%s %s %s admitted a non-owner: %d %s", name, route.method, route.path, w.Code, w.Body)
				}
			}
		}
		if w := notificationRequest(handler, "GET", f.owner, "/v1/admin/feedback/reports", ""); w.Code != 200 {
			t.Errorf("%s refused the owner's Feedback list: %d %s", name, w.Code, w.Body)
		}
	}
	for _, route := range []struct{ method, path string }{
		{"GET", "/v1/admin/diagnostics/client-logs"},
		{"GET", "/v1/admin/diagnostics/client-logs/unknown"},
		{"GET", "/v1/admin/diagnostics/client-logs/unknown/download"},
		{"DELETE", "/v1/admin/diagnostics/client-logs/unknown"},
	} {
		if w := f.call(route.method, route.path, f.admin, ""); w.Code != 403 {
			t.Errorf("app logs must remain owner-only: %s %s: %d %s", route.method, route.path, w.Code, w.Body)
		}
	}
}
