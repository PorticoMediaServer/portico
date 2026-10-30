package httpapi

import (
	"net/http"
	"testing"
)

func TestDashboardAndHistoryClassifyInvalidReportContextWithoutWeakeningAuthority(t *testing.T) {
	f := administrationFixtureNew(t)
	for _, path := range []string{"/v1/admin/dashboard/report", "/v1/admin/play-history", "/v1/admin/play-history/summary", "/v1/admin/play-history/dashboard", "/v1/admin/play-history/people"} {
		for _, query := range []string{"", "?timeZone=America%2FHalifax", "?timeZone=Local&until=2026-10-22T12%3A00%3A00Z", "?timeZone=America%2FHalifax&until=2026-10-22T12%3A01%3A00Z", "?timeZone=America%2FHalifax&until=2099-01-01T12%3A00%3A00Z"} {
			response := f.call("GET", path+query, f.owner, "")
			if response.Code != http.StatusBadRequest {
				t.Fatalf("owner invalid report context: %s %d %s", path+query, response.Code, response.Body)
			}
		}
		for _, token := range []string{f.admin, f.member} {
			response := f.call("GET", path, token, "")
			if response.Code != http.StatusForbidden {
				t.Fatalf("report context preceded authority: %s %d %s", path, response.Code, response.Body)
			}
		}
	}
}

func TestMCPPlayHistoryRequiresAndPreservesViewerReportContext(t *testing.T) {
	for _, tool := range mcpTools() {
		if tool.Name != "play_history" {
			continue
		}
		for _, args := range []map[string]any{{"period": "7d"}, {"period": "7d", "timeZone": "America/Halifax"}, {"period": "7d", "until": "2026-10-22T12:00:00Z"}} {
			if err := checkArguments(tool, args); err == nil {
				t.Fatal("missing viewer context accepted", args)
			}
		}
		args := map[string]any{"period": "7d", "timeZone": "America/Halifax", "until": "2026-10-22T12:00:00Z"}
		if err := checkArguments(tool, args); err != nil {
			t.Fatal(err)
		}
		called := false
		_, _, err := tool.Run(args, func(call mcpCall) (int, http.Header, []byte, error) {
			called = true
			if call.Path != "/v1/admin/play-history" || call.Query.Get("timeZone") != args["timeZone"] || call.Query.Get("until") != args["until"] || call.Query.Get("period") != "7d" {
				t.Fatal("tool altered viewer report context", call)
			}
			return http.StatusOK, nil, []byte(`{}`), nil
		})
		if err != nil || !called {
			t.Fatal("history route was not reached", called, err)
		}
		return
	}
	t.Fatal("play_history tool missing")
}
