package httpapi

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/administration"
	"github.com/PorticoMediaServer/portico/server/internal/lyrics"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestLegacyRevisionPresence(t *testing.T) {
	for _, body := range []string{`{}`, `{"expectedRevision":null}`, `{"expectedRevision":0}`, `{"expectedRevision":3}`, `{"expectedRevision":"3"}`} {
		var value struct {
			ExpectedRevision int64 `json:"expectedRevision"`
		}
		r := httptest.NewRequest("PUT", "/v1/admin/libraries/id/settings", strings.NewReader(body))
		err := decodeLegacyRevision(httptest.NewRecorder(), r, &value)
		missing := body == `{}` || strings.Contains(body, "null")
		if errors.Is(err, errRevisionRequired) != missing {
			t.Fatalf("%s: %v", body, err)
		}
		if (body == `{"expectedRevision":0}` || body == `{"expectedRevision":3}`) && err != nil {
			t.Fatal(err)
		}
		if body == `{"expectedRevision":"3"}` && err == nil {
			t.Fatal("string accepted")
		}
	}
}

func TestAdministrationMissingRevisionHTTP(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	d.Administration = administration.New(d.DB)
	h := New(d)
	for _, body := range []string{`{"operationId":"revision-test-key","settings":{}}`, `{"expectedRevision":null,"operationId":"revision-test-key","settings":{}}`} {
		r := httptest.NewRequest("PUT", "/v1/admin/libraries/missing/settings", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 428 || errorCode(t, w) != "revision_required" {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestMetadataMissingRevisionHTTP(t *testing.T) {
	d, viewer := logoutHTTPFixture(t)
	d.Metadata = metadata.New(d.DB, "")
	h := New(d)
	for _, route := range []struct{ method, path string }{
		{"PUT", "/v1/shows/missing/metadata/tvdb"},
		{"POST", "/v1/shows/missing/metadata/tvdb/retry"},
		{"PUT", "/v1/albums/missing/metadata/musicbrainz"},
		{"POST", "/v1/items/missing/metadata/local-audio/policy"},
		{"PUT", "/v1/libraries/missing/metadata/agent"},
		{"PUT", "/v1/libraries/missing/metadata/screen"},
	} {
		t.Run(route.path, func(t *testing.T) {
			r := httptest.NewRequest(route.method, route.path, strings.NewReader(`{}`))
			r.Header.Set("Authorization", "Bearer "+viewer.AccessToken)
			r.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 428 || errorCode(t, w) != "revision_required" {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestLyricAnalysisRevisionPresence(t *testing.T) {
	for _, factory := range []func() any{
		func() any { return &lyrics.Mutation{} },
		func() any { return &lyrics.SearchInput{} },
		func() any { return &mediaanalysis.Mutation{} },
	} {
		for _, body := range []string{`{}`, `{"expectedRevision":null}`, `{"expectedRevision":0}`} {
			r := httptest.NewRequest("POST", "/v1/items/id/lyrics", strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
			err := lyricDecode(httptest.NewRecorder(), r, factory())
			if body == `{"expectedRevision":0}` {
				if err != nil {
					t.Fatal(err)
				}
				continue
			}
			if !errors.Is(err, errRevisionRequired) {
				t.Fatalf("%s: %v", body, err)
			}
		}
	}
}
