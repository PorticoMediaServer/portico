package apikit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit/apierror"
)

func TestHomeRowPathAcceptsPublishedKeysWithoutWideningOtherIDs(t *testing.T) {
	for _, id := range []string{"continue", "view:AbC_123-xyz", "for_you:genre:science fiction", "for_you:genre:drame québécois", "for_you:person:tmdb:dv", "for_you:person:cd:directing:françois truffaut", "for_you:tag:tone:gritty", "for_you:franchise", "for_you:series", "for_you:rediscover", "for_you:short", "for_you:gems", "for_you:top"} {
		for _, encoded := range []bool{false, true} {
			path := "/v1/home/rows/" + url.PathEscape(id)
			if !encoded {
				path = strings.ReplaceAll(path, "%3A", ":")
			}
			values, _, ok := matchRoute("/v1/home/rows/{id}", path)
			if !ok || values["id"] != id {
				t.Errorf("published Home key %q encoded=%v did not match: %v %v", id, encoded, values, ok)
			}
			if strings.Contains(id, ":") {
				for _, pattern := range []string{"/v1/items/{id}", "/v1/home/rows/{id}:remove", "/v1/other/{id}"} {
					candidate := strings.Replace(pattern, "{id}", url.PathEscape(id), 1)
					if _, _, ok := matchRoute(pattern, candidate); ok {
						t.Errorf("Home key widened another wildcard: %s", candidate)
					}
				}
			}
		}
	}
	for _, segment := range []string{"view:", "view:a:b", "view:..", "other:abc", "for_you:", "for_you:unknown", "for_you:genre:", "for_you:genre:%20", "for_you:genre:..", "for_you:tag:tone%2Fgritty", "for_you:genre:a%5Cb", "for_you:genre:%00", "for_you:genre:%C2%AD", "for_you:genre:a%E2%80%A8b", "for_you:genre:%FF", "for_you:genre:a%253Ab", "view:a%2Fb", "view:a/b", "view:a%3Fb", "view:a%23b", "view:a%0Ab", "view:" + strings.Repeat("a", 124)} {
		if _, _, ok := matchRoute("/v1/home/rows/{id}", "/v1/home/rows/"+segment); ok {
			t.Errorf("invalid Home path segment matched: %q", segment)
		}
	}
}

func TestHomeRowExtendedKeyStillRunsCredentialGate(t *testing.T) {
	allowed, served := false, 0
	r := New(func(context.Context, *http.Request, Metadata) error {
		if !allowed {
			return &apierror.Error{Code: "unauthorized"}
		}
		return nil
	}, nil)
	m := testMetadata()
	m.ID, m.Method, m.Path, m.BodyLimit = "home_row", "GET", "/v1/home/rows/{id}", 0
	if err := Register(r, Route[struct{}, testResponse]{Metadata: m, Handler: func(_ context.Context, q *http.Request, _ struct{}) (testResponse, error) {
		served++
		return testResponse{Name: q.PathValue("id"), Items: []string{}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int{401, 200} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("GET", "/v1/home/rows/view%3AAbC_123-xyz", nil))
		if w.Code != expected {
			t.Fatalf("Home route credential gate: got %d, want %d", w.Code, expected)
		}
		if !allowed && served != 0 {
			t.Fatal("private Home handler ran before authorization")
		}
		allowed = true
	}
	if served != 1 {
		t.Fatalf("Home handler ran %d times", served)
	}
}
