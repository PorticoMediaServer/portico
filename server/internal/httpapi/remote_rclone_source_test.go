package httpapi

import (
	"bytes"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
)

func TestRcloneSourceRouteKeepsOwnerFirstAndNoSubsystemFacts(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		method string
		path   string
		token  string
		status int
	}{{"POST", "/v1/storage/sources/rclone", "", 401}, {"POST", "/v1/storage/sources/rclone", owner.AccessToken, 503},
		{"PUT", "/v1/storage/sources/rclone/source", "", 401}, {"PUT", "/v1/storage/sources/rclone/source", owner.AccessToken, 503},
		{"DELETE", "/v1/storage/sources/rclone/source", "", 401}, {"DELETE", "/v1/storage/sources/rclone/source", owner.AccessToken, 503}} {
		body := `{"operationId":"invalid","name":"Cloud","remote":"cloud:","config":"[cloud]\ntype=s3\nsecret_access_key=private-secret\n"}`
		if test.method == "PUT" {
			body = `{"expectedGeneration":1,` + body[1:]
		}
		if test.method == "DELETE" {
			body = `{"operationId":"invalid","expectedGeneration":1}`
		}
		r := httptest.NewRequest(test.method, test.path, bytes.NewBufferString(body))
		r.Header.Set("Content-Type", "application/json")
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		registry.ServeHTTP(w, r)
		if w.Code != test.status || strings.Contains(w.Body.String(), "private-secret") {
			t.Fatal("source save changed owner/absence boundary", w.Code)
		}
	}
}

func TestRcloneSourceFailureRetainsSafeReceiptCodes(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{mounts.ErrCommandConflict, "storage_command_conflict"},
		{mounts.ErrCommandExpired, "storage_operation_expired"},
		{mounts.ErrCommandCapacity, "storage_command_capacity"},
		{mounts.ErrAllocationCapacity, "storage_allocation_recovery_required"},
		{mounts.ErrSourceInUse, "request_in_progress"},
		{fmt.Errorf("https://owner:private-secret@private.example/: failed"), "internal"},
	} {
		var failure *apierror.Error
		projected := rcloneSourceFailure(test.err)
		if !errors.As(projected, &failure) || failure.Code != test.code || failure.Cause != nil || strings.Contains(projected.Error(), "private-secret") {
			t.Fatal("source save leaked a private cause or lost receipt code", test.code)
		}
	}
}
