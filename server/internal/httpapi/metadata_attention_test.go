package httpapi

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

func TestTypedMetadataAttentionAndSettingsRoutes(t *testing.T) {
	d, owner := logoutHTTPFixture(t)
	d.Metadata = metadata.New(d.DB, "")
	c := catalogtest.New(t, d.DB)
	library := c.Library("metadata-fixture-lib", "Films", "movie", "/metadata-fixture")
	item := c.Movie(library, "/metadata-fixture/Film.mp4", "Film", 2008)
	c.Drain()
	handler := New(d)
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		return w
	}
	settings := call("GET", "/v1/admin/metadata/settings", "", owner.AccessToken)
	if settings.Code != 200 {
		t.Fatal(settings.Code, settings.Body.String())
	}
	var current metadata.MetadataSettings
	if e := json.Unmarshal(settings.Body.Bytes(), &current); e != nil {
		t.Fatal(e)
	}
	token := "fixture-owner-token"
	body, _ := json.Marshal(metadata.MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: 4 << 30, TMDBReadToken: &token})
	saved := call("PUT", "/v1/admin/metadata/settings", string(body), owner.AccessToken)
	if saved.Code != 200 || strings.Contains(saved.Body.String(), token) {
		t.Fatal("settings response", saved.Code, saved.Body.String())
	}
	stale := call("PUT", "/v1/admin/metadata/settings", string(body), owner.AccessToken)
	if stale.Code != 412 {
		t.Fatal("stale settings accepted", stale.Code)
	}
	path := "/v1/libraries/metadata-fixture-lib/metadata/attention"
	list := call("GET", path, "", owner.AccessToken)
	if list.Code != 200 {
		t.Fatal(list.Code, list.Body.String())
	}
	var page metadata.AttentionPage
	if e := json.Unmarshal(list.Body.Bytes(), &page); e != nil || len(page.Entries) != 1 {
		t.Fatal(page, e)
	}
	body, _ = json.Marshal(metadata.AttentionCommand{Kind: "item", EntityID: item.Public, ExpectedRevision: page.Entries[0].Revision, Action: "dismiss"})
	if result := call("POST", path, string(body), owner.AccessToken); result.Code != 202 {
		t.Fatal(result.Code, result.Body.String())
	}
	result := call("POST", "/v1/items/"+item.Public+"/metadata/refresh", "{}", owner.AccessToken)
	if result.Code != 202 {
		t.Fatal(result.Code, result.Body.String())
	}
	for _, route := range []string{path, "/v1/admin/metadata/settings"} {
		if denied := call("GET", route, "", ""); denied.Code != 401 {
			t.Fatal("unauthed metadata route", route, denied.Code)
		}
	}
}
