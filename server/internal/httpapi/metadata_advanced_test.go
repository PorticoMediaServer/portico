package httpapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/metadata"
)

// Server › Metadata › Advanced round-trips through the owner's settings, the
// AcoustID key is write-only, and Refresh all is the owner's alone.
func TestMetadataAdvancedSettingsAndRefreshAllAuthority(t *testing.T) {
	f := administrationFixtureNew(t)
	f.deps.Metadata = metadata.New(f.deps.DB, "")
	f.handler = New(f.deps)
	w := f.call("GET", "/v1/admin/metadata/settings", f.owner, "")
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var current metadata.MetadataSettings
	if e := json.Unmarshal(w.Body.Bytes(), &current); e != nil {
		t.Fatal(e)
	}
	if current.RefreshPolicy != "regular" || current.FallbackLanguage != "en" || current.AnimeTitles != "language" || !current.CastPhotos || current.AcoustIDKeyConfigured || current.AcoustIDKeySource != "none" {
		t.Fatalf("spec defaults: %+v", current)
	}
	policy, fallback, anime, cast, key := "recent", "original", "romaji", false, "acoustid-owner-key"
	body, _ := json.Marshal(metadata.MetadataSettingsUpdate{ExpectedRevision: current.Revision, LocalArtworkLimitBytes: current.LocalArtworkLimitBytes,
		RefreshPolicy: &policy, FallbackLanguage: &fallback, AnimeTitles: &anime, CastPhotos: &cast, AcoustIDKey: &key})
	w = f.call("PUT", "/v1/admin/metadata/settings", f.owner, string(body))
	if w.Code != 200 || strings.Contains(w.Body.String(), key) {
		t.Fatal("save", w.Code, w.Body.String())
	}
	var saved metadata.MetadataSettings
	if e := json.Unmarshal(w.Body.Bytes(), &saved); e != nil {
		t.Fatal(e)
	}
	if saved.RefreshPolicy != policy || saved.FallbackLanguage != fallback || saved.AnimeTitles != anime || saved.CastPhotos || !saved.AcoustIDKeyConfigured || saved.AcoustIDKeySource != "owner" {
		t.Fatalf("saved: %+v", saved)
	}
	bad := "weekly"
	body, _ = json.Marshal(metadata.MetadataSettingsUpdate{ExpectedRevision: saved.Revision, LocalArtworkLimitBytes: saved.LocalArtworkLimitBytes, RefreshPolicy: &bad})
	if w = f.call("PUT", "/v1/admin/metadata/settings", f.owner, string(body)); w.Code != 400 {
		t.Fatal("unknown refresh policy accepted", w.Code, w.Body.String())
	}
	empty := ""
	body, _ = json.Marshal(metadata.MetadataSettingsUpdate{ExpectedRevision: saved.Revision, LocalArtworkLimitBytes: saved.LocalArtworkLimitBytes, AcoustIDKey: &empty})
	if w = f.call("PUT", "/v1/admin/metadata/settings", f.owner, string(body)); w.Code != 200 || !strings.Contains(w.Body.String(), `"acoustidKeyConfigured":false`) {
		t.Fatal("clearing the owner key", w.Code, w.Body.String())
	}

	for _, token := range []string{f.admin, f.member} {
		if w = f.call("POST", "/v1/admin/metadata/settings/refresh-all", token, ""); w.Code != 403 {
			t.Fatal("refresh all is the owner's", w.Code, w.Body.String())
		}
		if w = f.call("PUT", "/v1/admin/metadata/settings", token, string(body)); w.Code != 403 {
			t.Fatal("metadata settings are the owner's", w.Code, w.Body.String())
		}
	}
	if w = f.call("POST", "/v1/admin/metadata/settings/refresh-all", "", ""); w.Code != 401 {
		t.Fatal("unauthenticated refresh all", w.Code)
	}
	if w = f.call("POST", "/v1/admin/metadata/settings/refresh-all", f.owner, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"running":true`) {
		t.Fatal("owner refresh all", w.Code, w.Body.String())
	}
}
