package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
)

func TestHomeDestinationReceiptHTTPRefusesForeignJobSetsAndUnboundedReads(t *testing.T) {
	handler, owner, _ := editorHTTPFixture(t)
	body, _ := json.Marshal(catalog.HomeDestinationReceiptRequest{Kind: "playlist", DestinationID: "unknown", JobIDs: []string{"foreign-job"}})
	response := editorRequest(t, handler, "POST", "/v1/home/destination-receipt", owner, "application/json", body)
	if response.Code != http.StatusNotFound || errorCode(t, response) != "not_found" || strings.Contains(response.Body.String(), "foreign-job") {
		t.Fatalf("receipt route missing or foreign jobs disclosed: %d %s", response.Code, response.Body.String())
	}
	anonymous := editorRequest(t, handler, "POST", "/v1/home/destination-receipt", "", "application/json", body)
	if anonymous.Code != http.StatusUnauthorized || errorCode(t, anonymous) != "unauthorized" {
		t.Fatalf("anonymous receipt read: %d %s", anonymous.Code, anonymous.Body.String())
	}
	ids := make([]string, 201)
	for index := range ids {
		ids[index] = "job"
	}
	invalid, _ := json.Marshal(catalog.HomeDestinationReceiptRequest{Kind: "playlist", DestinationID: "unknown", JobIDs: ids})
	oversized := editorRequest(t, handler, "POST", "/v1/home/destination-receipt", owner, "application/json", invalid)
	if oversized.Code != http.StatusBadRequest || errorCode(t, oversized) != "invalid_request" {
		t.Fatalf("unbounded receipt read: %d %s", oversized.Code, oversized.Body.String())
	}
}
