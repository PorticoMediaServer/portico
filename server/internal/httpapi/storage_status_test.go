package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestStorageStatusBaselineContractAndAuthority(t *testing.T) {
	f := newTL11AdmissionFixture(t, "movie")
	handler := New(f.d)
	response := logoutHTTPRequest(handler, "GET", "/v1/admin/storage/status", f.token)
	if response.Code != http.StatusOK {
		t.Fatalf("owner status: %d %s", response.Code, response.Body.String())
	}
	var out map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if _, exists := out["upgradeCopies"]; exists {
		t.Fatal("retired upgrade-copy contract remains")
	}
	for _, key := range []string{"databasePath", "cacheDirectory", "backupDirectory", "reclaimableBytes", "lastReclaimedBytes"} {
		if _, exists := out[key]; !exists {
			t.Errorf("missing storage fact %s", key)
		}
	}
	if denied := logoutHTTPRequest(handler, "GET", "/v1/admin/storage/status", ""); denied.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous storage status: %d", denied.Code)
	}
	retired := logoutHTTPRequest(handler, "DELETE", "/v1/admin/storage/upgrade-copies/before-20261013T120000.000000000Z.sqlite", f.token)
	if retired.Code != http.StatusNotFound {
		t.Fatalf("retired upgrade route: %d %s", retired.Code, retired.Body.String())
	}
}
