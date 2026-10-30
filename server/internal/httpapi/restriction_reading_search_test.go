package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Both registered search routes exercise real indexed publication text, then
// refuse the same now-hidden source under the current profile restrictions.
func readingBodySearchRestrictionCheck(t *testing.T) {
	r := newReadingFixture(t, "book", "epub")
	ctx := context.Background()
	readingSeriesDrain(t, r)
	if err := r.s.PrepareTextIndex(ctx, r.library, r.item, r.source); err != nil {
		t.Fatal(err)
	}
	readingSeriesDrain(t, r)
	h := New(r.d)
	owner := readingOwnerLogin(t, r)
	profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Search reader", "blue")
	if err != nil {
		t.Fatal(err)
	}
	var child identity.DirectProfile
	for _, profile := range profiles.Profiles {
		if profile.Name == "Search reader" && !profile.Primary {
			child = profile
		}
	}
	if child.ID == "" {
		t.Fatal("actual child profile is absent")
	}
	selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
	if err != nil {
		t.Fatal(err)
	}
	token := selected.Session.AccessToken
	opened := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{r.item, r.source})
	var manifest ReadingSessionResponse
	if opened.Code != 200 || json.Unmarshal(opened.Body.Bytes(), &manifest) != nil || manifest.SessionID == "" {
		t.Fatal("actual source open", opened.Code, opened.Body.String())
	}
	readingSeriesDrain(t, r)
	paths := []string{"/v1/reading/search?q=original&limit=1", "/v1/reading/sessions/" + manifest.SessionID + "/search?q=original&limit=1"}
	for _, path := range paths {
		if w := readingHTTP(t, h, "GET", path, "", "", nil); w.Code != 401 {
			t.Fatal("anonymous search admitted", w.Code, w.Body.String())
		}
		w := readingHTTP(t, h, "GET", path, token, manifest.Revision, nil)
		var page struct {
			Hits []json.RawMessage `json:"hits"`
		}
		if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Hits) != 1 || !strings.Contains(w.Body.String(), r.source) {
			for _, table := range []string{"reading_text_index_state", "reading_text_sections", "reading_attribution_members"} {
				var count int
				if err := r.f.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
					t.Log(table, err)
				} else {
					t.Log(table, count)
				}
			}
			var input, ready, rule int64
			err := r.f.db.QueryRow(`SELECT input_generation,ready_generation,publication_rule_version FROM reading_attribution_state`).Scan(&input, &ready, &rule)
			t.Log("ready state", input, ready, rule, err)
			t.Fatal("indexed visible control", w.Code, w.Body.String())
		}
	}
	if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
		t.Fatal(err)
	}
	for index, path := range paths {
		w := readingHTTP(t, h, "GET", path, token, manifest.Revision, nil)
		for _, hidden := range []string{r.item, r.source, manifest.SessionID, "This original text"} {
			if strings.Contains(w.Body.String(), hidden) {
				t.Fatal("restricted search disclosed publication", w.Code, w.Body.String())
			}
		}
		if index == 0 {
			var page struct {
				Hits []json.RawMessage `json:"hits"`
			}
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Hits) != 0 {
				t.Fatal("restricted global search", w.Code, w.Body.String())
			}
		} else {
			// Keep the exact owned session/actor while making its underlying title
			// absent. An invented session token changes authority, not just visibility.
			if _, err := r.f.db.Exec(`UPDATE catalog_entities SET retired=1 WHERE pid(public_id)=?`, r.item); err != nil {
				t.Fatal(err)
			}
			missing := readingHTTP(t, h, "GET", path, token, manifest.Revision, nil)
			if w.Code != 404 || missing.Code != w.Code || bodyErrorCode(w.Body.String()) != bodyErrorCode(missing.Body.String()) {
				t.Fatal("hidden title differs from absent under the same owned session", w.Code, w.Body.String(), missing.Code, missing.Body.String())
			}
			unknown := readingHTTP(t, h, "GET", strings.Replace(path, manifest.SessionID, identity.Token(), 1), token, manifest.Revision, nil)
			if unknown.Code != 403 {
				t.Fatal("unowned session token was admitted", unknown.Code, unknown.Body.String())
			}
		}
	}
}
