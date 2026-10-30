package httpapi

import (
	"encoding/json"
	"net/http"
	"github.com/PorticoMediaServer/portico/server/internal/rcloneprogram"
	"testing"
)

func TestBundledRcloneStartupSnapshotAndAuthority(t *testing.T) {
	f := administrationFixtureNew(t)
	status := rcloneprogram.Status{Included: true, Available: true, Version: rcloneprogram.BundledVersion, MinimumVersion: rcloneprogram.MinimumVersion}
	f.deps.RcloneProgram = &status
	handler := New(f.deps)
	for _, tc := range []struct {
		token  string
		status int
	}{{"", http.StatusUnauthorized}, {f.member, http.StatusForbidden}, {f.admin, http.StatusForbidden}} {
		response := logoutHTTPRequest(handler, "GET", "/v1/storage/rclone/bundled", tc.token)
		if response.Code != tc.status {
			t.Fatalf("unauthorized token returned bundled status: %s", response.Body.String())
		}
	}
	response := logoutHTTPRequest(handler, "GET", "/v1/storage/rclone/bundled", f.owner)
	if response.Code != http.StatusOK {
		t.Fatalf("%d %s", response.Code, response.Body.String())
	}
	var got rcloneprogram.Status
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got != status {
		t.Fatalf("%+v %v", got, err)
	}
	if response = logoutHTTPRequest(handler, "GET", "/v1/storage/rclone/bundled?probe=true", f.owner); response.Code != http.StatusBadRequest {
		t.Fatalf("query: %d", response.Code)
	}
	status.Available = false
	status.Version = ""
	status.ErrorCode = "cant_start_rclone"
	response = logoutHTTPRequest(handler, "GET", "/v1/storage/rclone/bundled", f.owner)
	if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || got != status {
		t.Fatalf("failed bundled status: %+v %v", got, err)
	}
}
