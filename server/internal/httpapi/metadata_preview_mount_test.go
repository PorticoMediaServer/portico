package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/PorticoMediaServer/portico/apikit"
)

func TestMetadataPreviewMountUsesCanonicalScopeAndRejectsOtherActions(t *testing.T) {
	registry, err := FoundationRegistry(Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	d := Dependencies{registry: registry}
	mux := http.NewServeMux()
	called := 0
	d.httpHandle(mux, "GET /v1/metadata/{kind}/{id}/preview", func(w http.ResponseWriter, r *http.Request) {
		called++
		if r.PathValue("kind") != "item" || r.PathValue("id") != "example" {
			t.Fatal("lost path binding")
		}
		w.WriteHeader(http.StatusNoContent)
	})
	// The two real public paths are compatible; neither declaration is erased.
	mux.HandleFunc("GET /v1/metadata/bulk/preview-queries/{queryId}", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusAccepted) })
	q := httptest.NewRequest("GET", "/v1/metadata/item/example/preview", nil)
	if scope, matched := d.routeScope(mux, q); !matched || scope != apikit.ScopeLibrary {
		t.Fatal("canonical scope lost through mount", scope, matched)
	}
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, q)
	if w.Code != http.StatusNoContent || called != 1 {
		t.Fatal(w.Code, called)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/metadata/item/example/other", nil))
	if w.Code != http.StatusNotFound || called != 1 {
		t.Fatal("adapter reached noncontract action", w.Code, called)
	}
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", "/v1/metadata/bulk/preview-queries/query", nil))
	if w.Code != http.StatusAccepted || called != 1 {
		t.Fatal("bulk query dispatch lost", w.Code, called)
	}
}
