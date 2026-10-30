package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func readingSavedRestrictionCheck(t *testing.T) {
	for _, format := range []ReadingFormat{"pdf", "cbz"} {
		t.Run(string(format), func(t *testing.T) {
			kind := "book"
			if format == "cbz" {
				kind = "comic"
			}
			r := newReadingFixture(t, kind, string(format))
			h := New(r.d)
			ctx := context.Background()
			owner := readingOwnerLogin(t, r)
			profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Mark reader", "blue")
			if err != nil {
				t.Fatal(err)
			}
			var child identity.DirectProfile
			for _, p := range profiles.Profiles {
				if !p.Primary && p.Name == "Mark reader" {
					child = p
				}
			}
			if child.ID == "" {
				t.Fatal("no real child profile")
			}
			selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
			if err != nil {
				t.Fatal(err)
			}
			token := selected.Session.AccessToken
			path := func(id string) string { return "/v1/items/" + id + "/reading/my-list" }
			get := readingHTTP(t, h, "GET", path(r.item), token, "", nil)
			var initial ReadingSavedResponse
			if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &initial) != nil || initial.ItemID != r.item || initial.ServerID != r.f.id.ID() || initial.ViewerFence == "" || initial.Listed {
				t.Fatal("real initial mark", get.Code, get.Body.String())
			}
			opened := readingHTTP(t, h, "POST", "/v1/reading/sessions", token, "", ReadingSessionRequest{r.item, r.source})
			var manifest ReadingSessionResponse
			if opened.Code != 200 || json.Unmarshal(opened.Body.Bytes(), &manifest) != nil || manifest.SessionID == "" {
				t.Fatal("actual Reading session", opened.Code, opened.Body.String())
			}
			page := int64(0)
			savedLocation := readingHTTP(t, h, "PUT", "/v1/reading/sessions/"+manifest.SessionID+"/progress", token, manifest.Revision, ReadingProgressRequest{Location: ReadingLocation{Format: format, PageIndex: &page, Progression: .25}})
			if savedLocation.Code != 200 {
				t.Fatal("actual saved location", savedLocation.Code, savedLocation.Body.String())
			}
			before := readingRestrictionProgress(t, r)
			if before == "null" || before == "[]" {
				t.Fatal("saved-location control was vacuous")
			}
			yes := true
			body := ReadingSavedRequest{OperationID: "matrix-reading-save", ExpectedRevision: initial.Revision, Listed: &yes}
			put := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var marked ReadingSavedResponse
			if put.Code != 200 || json.Unmarshal(put.Body.Bytes(), &marked) != nil || !marked.Listed || marked.ItemID != r.item || marked.OperationID != body.OperationID || marked.Revision <= initial.Revision {
				t.Fatal("real My List membership", put.Code, put.Body.String())
			}
			replay := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var replayed ReadingSavedResponse
			if replay.Code != 200 || json.Unmarshal(replay.Body.Bytes(), &replayed) != nil || replayed.Revision != marked.Revision || !replayed.Listed {
				t.Fatal("immutable operation replay", replay.Code, replay.Body.String())
			}
			readingSeriesDrain(t, r)
			savedPage := readingHTTP(t, h, "GET", "/v1/content?view=watchlist&shape=posters&limit=1", token, "", nil)
			var saved catalog.ContentEnvelope
			if savedPage.Code != 200 || json.Unmarshal(savedPage.Body.Bytes(), &saved) != nil || len(saved.Sections) != 1 || len(saved.Sections[0].Entries) != 1 || saved.Sections[0].TotalCount != 1 || saved.ShapeCounts == nil || saved.ShapeCounts.Posters != 1 {
				t.Fatal("Reading title absent from actual Saved page/counts", savedPage.Code, savedPage.Body.String())
			}
			card := saved.Sections[0].Entries[0]
			wantKind := "ebook"
			if kind == "comic" {
				wantKind = "comic"
			}
			if card.ID != r.item || card.Kind != wantKind || card.LibraryID != r.library || card.Playback != nil || card.Duration != nil || card.Navigation == nil || card.Navigation.View != "item" || card.Navigation.EntityID != r.item || card.Watchlisted == nil || !*card.Watchlisted {
				t.Fatal("Saved Reading card identity/state/playback meaning", savedPage.Body.String())
			}
			inProgress := readingHTTP(t, h, "GET", "/v1/content?view=watchlist&shape=posters&filter=inProgress&limit=1", token, "", nil)
			var progressing catalog.ContentEnvelope
			if inProgress.Code != 200 || json.Unmarshal(inProgress.Body.Bytes(), &progressing) != nil || len(progressing.Sections) != 1 || progressing.Sections[0].TotalCount != 1 || len(progressing.Sections[0].Entries) != 1 || progressing.Sections[0].Entries[0].ID != r.item {
				t.Fatal("actual Reading location absent from In progress", inProgress.Code, inProgress.Body.String())
			}
			if _, err = r.f.db.Exec(`UPDATE reading_progress SET source_revision=? WHERE owner_key=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, strings.Repeat("0", 64), identity.PersonalKey(selected.Session.Viewer), r.item); err != nil {
				t.Fatal(err)
			}
			stale := readingHTTP(t, h, "GET", "/v1/content?view=watchlist&shape=posters&filter=inProgress&limit=1", token, "", nil)
			var stalePage catalog.ContentEnvelope
			if stale.Code != 200 || json.Unmarshal(stale.Body.Bytes(), &stalePage) != nil || len(stalePage.Sections) != 0 {
				t.Fatal("stale Reading source counted as In progress", stale.Code, stale.Body.String())
			}
			if _, err = r.f.db.Exec(`UPDATE reading_progress SET source_revision=? WHERE owner_key=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, manifest.Revision, identity.PersonalKey(selected.Session.Viewer), r.item); err != nil {
				t.Fatal(err)
			}
			no := false
			later := readingHTTP(t, h, "PUT", path(r.item), token, "", ReadingSavedRequest{OperationID: "matrix-reading-unsave", ExpectedRevision: marked.Revision, Listed: &no})
			var unmarked ReadingSavedResponse
			if later.Code != 200 || json.Unmarshal(later.Body.Bytes(), &unmarked) != nil || unmarked.Listed || unmarked.Revision <= marked.Revision {
				t.Fatal("later explicit removal", later.Code, later.Body.String())
			}
			old := readingHTTP(t, h, "PUT", path(r.item), token, "", body)
			var oldReceipt ReadingSavedResponse
			if old.Code != 200 || json.Unmarshal(old.Body.Bytes(), &oldReceipt) != nil || !oldReceipt.Listed || oldReceipt.Revision != marked.Revision || oldReceipt.OperationID != body.OperationID {
				t.Fatal("old receipt changed after later intent", old.Code, old.Body.String())
			}
			latest := readingHTTP(t, h, "GET", path(r.item), token, "", nil)
			var currentMark ReadingSavedResponse
			if latest.Code != 200 || json.Unmarshal(latest.Body.Bytes(), &currentMark) != nil || currentMark.Listed || currentMark.Revision != unmarked.Revision {
				t.Fatal("replay replaced later explicit choice", latest.Code, latest.Body.String())
			}
			removed := readingHTTP(t, h, "GET", "/v1/content?view=watchlist&shape=posters&limit=1", token, "", nil)
			var removedPage catalog.ContentEnvelope
			if removed.Code != 200 || json.Unmarshal(removed.Body.Bytes(), &removedPage) != nil || len(removedPage.Sections) != 0 || removedPage.ShapeCounts == nil || removedPage.ShapeCounts.Posters != 0 {
				t.Fatal("unlisted Reading title remained in Saved", removed.Code, removed.Body.String())
			}
			if readingRestrictionProgress(t, r) != before {
				t.Fatal("My List changed saved Reading location")
			}
			other := readingHTTP(t, h, "GET", path(r.item), r.f.owner.AccessToken, "", nil)
			var otherMark ReadingSavedResponse
			if other.Code != 200 || json.Unmarshal(other.Body.Bytes(), &otherMark) != nil || otherMark.Listed {
				t.Fatal("My List escaped its exact profile", other.Code, other.Body.String())
			}
			for _, method := range []string{"GET", "PUT"} {
				if w := readingHTTP(t, h, method, path(r.item), "", "", body); w.Code != 401 {
					t.Fatal("anonymous My List", w.Code, w.Body.String())
				}
			}
			refuse := func() {
				t.Helper()
				for _, method := range []string{"GET", "PUT"} {
					hidden := readingHTTP(t, h, method, path(r.item), token, "", body)
					absent := readingHTTP(t, h, method, path(absentTwin(r.item)), token, "", body)
					if hidden.Code != 404 || absent.Code != 404 || normalizedAnswer(hidden.Body.String(), r.item) != normalizedAnswer(absent.Body.String(), absentTwin(r.item)) {
						t.Fatal("hidden/absent My List differs", method, hidden.Code, hidden.Body.String(), absent.Code, absent.Body.String())
					}
					for _, marker := range []string{r.item, r.source, r.library, r.path} {
						if marker != "" && strings.Contains(hidden.Body.String(), marker) {
							t.Fatal("hidden My List leaked target", hidden.Body.String())
						}
					}
				}
				if readingRestrictionProgress(t, r) != before {
					t.Fatal("refused My List changed saved Reading location")
				}
			}
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
				t.Fatal(err)
			}
			refuse()
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
				t.Fatal(err)
			}
			if _, err = r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectProfileEdit{ExpectedRevision: child.Revision, Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}}); err != nil {
				t.Fatal(err)
			}
			refuse()
		})
	}
}
