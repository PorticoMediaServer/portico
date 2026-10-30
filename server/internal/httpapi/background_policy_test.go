package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"github.com/PorticoMediaServer/portico/apikit"
	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"strings"
	"testing"
	"time"
)

func TestBackgroundPauseAndResourceUsageOwnerContracts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	defer dbwork.SetBackgroundPolicy(dbwork.SpeedNormal, 0)
	for _, role := range []string{"owner", "member"} {
		t.Run(role, func(t *testing.T) {
			d, session, _ := hostedAdministrationFixture(t, role)
			d.Administration = administration.New(d.DB)
			mux := http.NewServeMux()
			d.foundationRoutes(mux)
			request := httptest.NewRequest("POST", "/v1/admin/maintenance/background-pause", strings.NewReader(`{"paused":true,"operationId":"owner-pause-hour"}`))
			request.Header.Set("Authorization", "Bearer "+session.AccessToken)
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			mux.ServeHTTP(response, request)
			if role != "owner" {
				if response.Code < 400 {
					t.Fatal("member paused background", response.Code)
				}
				return
			}
			if response.Code != 200 {
				t.Fatal(response.Code, response.Body.String())
			}
			var document administration.MaintenanceDocument
			if err := json.Unmarshal(response.Body.Bytes(), &document); err != nil {
				t.Fatal(err)
			}
			if document.PausedUntil < time.Now().Add(59*time.Minute).UnixMilli() {
				t.Fatal("pause deadline missing", document)
			}
			usage := supportRequest(mux, session.AccessToken, "/v1/admin/resource-usage")
			if usage.Code != 200 {
				t.Fatal(usage.Code, usage.Body.String())
			}
			var out ResourceUsage
			if err := json.Unmarshal(usage.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Analysis.LimitBytes != 32<<30 {
				t.Fatal("usage did not publish owner limit", out)
			}
			registry, err := FoundationRegistry(d)
			if err != nil {
				t.Fatal(err)
			}
			for _, route := range registry.Definitions() {
				if strings.Contains(route.Path, "background-pause") || route.Path == "/v1/admin/resource-usage" {
					if route.Requires != apikit.ScopeAdmin {
						t.Fatal("unscoped route", route)
					}
				}
			}
		})
	}
}
