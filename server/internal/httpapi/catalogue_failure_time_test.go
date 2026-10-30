package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestCatalogueFailureInstantUsesTheGeneralWireConvention(t *testing.T) {
	d, _, owner, _ := notificationFixture(t)
	mux := http.NewServeMux()
	d.foundationRoutes(mux)
	if _, err := d.DB.Exec(`INSERT INTO catalog_derivation_failures(domain,entity_id,reason,failed_ms) VALUES(18,999,'Unreadable title',1760000000123)`); err != nil {
		t.Fatal(err)
	}
	w := notificationRequest(mux, "GET", owner.AccessToken, "/v1/admin/catalogue/failures", "")
	var out struct {
		Failures []map[string]any `json:"failures"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil || w.Code != 200 || len(out.Failures) != 1 {
		t.Fatalf("response %d %s: %v", w.Code, w.Body.String(), err)
	}
	if got := out.Failures[0]["failedAt"]; got != "2025-10-09T08:53:20.123Z" {
		t.Fatalf("failedAt must be RFC 3339 UTC, got %v", got)
	}
}
