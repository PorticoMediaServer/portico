package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/remotesources"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestDAVConnectionHTTPChecksOwnerAndNeverPublishesBlockedTest(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	helper, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	d.Mounts, err = mounts.New(d.DB, filepath.Join(state, "managed"), "", helper, storage.New(helper))
	if err != nil {
		t.Fatal(err)
	}
	d.RemoteSources, err = remotesources.New(d.DB, state, d.Mounts)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := FoundationRegistry(d)
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range registry.Definitions() {
		if route.ID != "test_storage_source_webdav" {
			continue
		}
		if spec := laneSpecs[route.Admission]; spec.budget != 0 || !spec.endsWithClient {
			t.Fatal("bounded connection test was assigned a shorter request deadline")
		}
	}
	body, _ := json.Marshal(remotesources.DAVConnectionInput{Name: "Archive", Root: "https://127.0.0.1/media/", Username: "fixture", Password: "private-secret"})
	for _, test := range []struct {
		token  string
		status int
	}{{"", 401}, {owner.AccessToken, 400}} {
		r := httptest.NewRequest("POST", "/v1/storage/sources/webdav/test", bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		if test.token != "" {
			r.Header.Set("Authorization", "Bearer "+test.token)
		}
		w := httptest.NewRecorder()
		registry.ServeHTTP(w, r)
		if w.Code != test.status || strings.Contains(w.Body.String(), "private-secret") || strings.Contains(w.Body.String(), "127.0.0.1") {
			t.Fatal("connection test failed owner/endpoint/privacy boundary", w.Code, w.Body.String())
		}
	}
	for _, table := range []string{"remote_sources", "remote_source_operations", "managed_mounts"} {
		var count int
		if err = d.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("test published durable state", table, count, err)
		}
	}
}

func TestDAVConnectionFailureDoesNotRetainCredentialsOrTransportText(t *testing.T) {
	for _, test := range []struct {
		err  error
		code string
	}{
		{fmt.Errorf("https://fixture:private-secret@nas.example/media/: dial failed"), "internal"},
		{fmt.Errorf("private-secret: %w", storage.ErrRemoteCredentials), "source_credentials_required"},
		{fmt.Errorf("private-secret: %w", storage.ErrRemoteConfig), "invalid_request"},
		{fmt.Errorf("private-secret: %w", mounts.ErrUnavailable), "administration_unavailable"},
		{fmt.Errorf("private-secret: %w", identity.ErrUnauthorized), "unauthorized"},
		{fmt.Errorf("private-secret: %w", identity.ErrForbidden), "not_permitted"},
	} {
		out := davConnectionFailure(test.err)
		var public *apierror.Error
		if !errors.As(out, &public) || public.Code != test.code || public.Cause != nil || strings.Contains(out.Error(), "private-secret") {
			t.Fatal("connection error retained private transport text", public)
		}
	}
}
