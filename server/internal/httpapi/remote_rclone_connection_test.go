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

func TestRcloneConnectionRouteKeepsOwnerFirstAndNoSubsystemFacts(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.AccessToken, 503}} {
		r := httptest.NewRequest("POST", "/v1/storage/sources/rclone/test", bytes.NewBufferString(`{"remote":"cloud:","config":"[cloud]\ntype=s3\nsecret_access_key=private-secret\n"}`))
		r.Header.Set("Content-Type", "application/json")
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		registry.ServeHTTP(w, r)
		if w.Code != test.status || strings.Contains(w.Body.String(), "private-secret") {
			t.Fatal("test route changed owner/absence boundary", w.Code)
		}
	}
}

func TestRcloneConnectionFailureDiscardsPrivateTransportCause(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{{fmt.Errorf("https://owner:private-secret@private.example/: failed"), "internal"}, {mounts.ErrConfigInvalid, "invalid_request"}, {mounts.ErrExecutableInvalid, "administration_unavailable"}} {
		projected := rcloneConnectionFailure(test.err)
		var failure *apierror.Error
		if !errors.As(projected, &failure) || failure.Code != test.code || failure.Cause != nil || strings.Contains(projected.Error(), "private-secret") {
			t.Fatal("connection failure exposed a private cause", test.code)
		}
	}
}
