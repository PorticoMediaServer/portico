package httpapi

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/contentaccess"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func readingSavedGroupRestrictionCheck(t *testing.T) {
	for _, kind := range []catalog.ReadingSavedGroupKind{catalog.ReadingSavedSeries, catalog.ReadingSavedMagazine, catalog.ReadingSavedNewspaper} {
		t.Run(string(kind), func(t *testing.T) {
			r := newReadingFixture(t, "book", "pdf")
			ctx := context.Background()
			name := "Private Reading group"
			paths := []string{readingTreeFile(t, r, name+"/Issue 1929-11-01.epub"), readingTreeFile(t, r, name+"/Issue 1929-12-01.epub")}
			readingScan(t, r.f, r.library)
			items := []string{readingPathItem(t, r, paths[0]), readingPathItem(t, r, paths[1])}
			for _, item := range items {
				readingSeriesOwnerField(t, r, item, "series", name)
				if kind != catalog.ReadingSavedSeries {
					readingSeriesOwnerField(t, r, item, "publicationType", string(kind))
				}
			}
			readingSeriesDrain(t, r)
			h := New(r.d)
			owner := readingOwnerLogin(t, r)
			profiles, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Group reader", "blue")
			if err != nil {
				t.Fatal(err)
			}
			var child identity.DirectProfile
			for _, profile := range profiles.Profiles {
				if !profile.Primary && profile.Name == "Group reader" {
					child = profile
				}
			}
			if child.ID == "" {
				t.Fatal("missing actual selected profile")
			}
			selected, err := r.f.id.SelectDirectProfile(ctx, owner.AccountToken, child.ID, identity.DirectSelection{})
			if err != nil {
				t.Fatal(err)
			}
			token := selected.Session.AccessToken
			profile := identity.PersonalKey(selected.Session.Viewer)
			series := compactcatalog.ReadingSeriesID(r.library, name)
			path := func(group string) string {
				return "/v1/libraries/" + r.library + "/reading/groups/" + string(kind) + "/" + group + "/my-list"
			}
			get := readingHTTP(t, h, "GET", path(series), token, "", nil)
			var initial ReadingSavedGroupResponse
			if get.Code != 200 || json.Unmarshal(get.Body.Bytes(), &initial) != nil || initial.Listed || initial.Revision != 0 || initial.ReferenceID != "" || initial.SeriesID != series || initial.LibraryID != r.library || initial.GroupKind != kind {
				t.Fatal("actual unsaved group", get.Code, get.Body.String())
			}
			var rows int
			if err = r.f.db.QueryRow(`SELECT count(*) FROM reading_saved_groups WHERE owner_key=?`, profile).Scan(&rows); err != nil || rows != 0 {
				t.Fatal("status minted a private reference", rows, err)
			}
			yes, no := true, false
			add := ReadingSavedGroupRequest{OperationID: "group-add", ExpectedRevision: 0, Listed: &yes}
			w := readingHTTP(t, h, "PUT", path(series), token, "", add)
			var added ReadingSavedGroupResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &added) != nil || !added.Listed || added.Revision != 1 || added.ReferenceID == "" || added.OperationID != add.OperationID {
				t.Fatal("actual group save", w.Code, w.Body.String())
			}
			for _, item := range items {
				state, err := r.f.cat.Personal(profile, item)
				if err != nil || state.Watchlisted {
					t.Fatal("group save expanded into leaf flags", err, state)
				}
			}
			// A later accepted member is in the same dynamic group, not in a saved array.
			third := readingTreeFile(t, r, name+"/Issue 1930-01-01.epub")
			readingScan(t, r.f, r.library)
			thirdItem := readingPathItem(t, r, third)
			readingSeriesOwnerField(t, r, thirdItem, "series", name)
			if kind != catalog.ReadingSavedSeries {
				readingSeriesOwnerField(t, r, thirdItem, "publicationType", string(kind))
			}
			readingSeriesDrain(t, r)
			p, err := r.f.id.Authenticate(token)
			if err != nil {
				t.Fatal(err)
			}
			err = dbwork.WithReadSnapshot(ctx, r.f.db, func(ctx context.Context) error {
				tx := dbwork.Snapshot(ctx)
				scope, err := r.d.readingSeriesScopeTx(ctx, tx, p, r.library)
				if err != nil {
					return err
				}
				restrictions, err := contentaccess.RestrictionsTx(ctx, tx, p.Viewer)
				if err != nil {
					return err
				}
				group, err := catalog.ResolveReadingSavedGroupTx(ctx, tx, catalog.Viewer{Profile: profile, Fence: scope.Fence, Libraries: []string{r.library}, Restrictions: restrictions}, catalog.ReadingSavedGroupTarget{LibraryID: r.library, GroupKind: kind, SeriesID: series}, false)
				if err == nil && (group.Count != 3 || group.Title != name || group.Proof.Key == "") {
					t.Fatal("dynamic group did not use complete current membership", group)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			remove := ReadingSavedGroupRequest{OperationID: "group-remove", ExpectedRevision: 1, Listed: &no}
			w = readingHTTP(t, h, "PUT", path(series), token, "", remove)
			var removed ReadingSavedGroupResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &removed) != nil || removed.Listed || removed.Revision != 2 || removed.ReferenceID != added.ReferenceID {
				t.Fatal("group tombstone", w.Code, w.Body.String())
			}
			w = readingHTTP(t, h, "PUT", path(series), token, "", add)
			var replay ReadingSavedGroupResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &replay) != nil || !replay.Listed || replay.Revision != 1 || replay.ReferenceID != added.ReferenceID {
				t.Fatal("immutable old group receipt", w.Code, w.Body.String())
			}
			w = readingHTTP(t, h, "GET", path(series), token, "", nil)
			var current ReadingSavedGroupResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &current) != nil || current.Listed || current.Revision != 2 {
				t.Fatal("replay changed current group state", w.Code, w.Body.String())
			}
			w = readingHTTP(t, h, "PUT", path(series), token, "", ReadingSavedGroupRequest{OperationID: "group-readd", ExpectedRevision: 2, Listed: &yes})
			var readded ReadingSavedGroupResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &readded) != nil || !readded.Listed || readded.Revision != 3 || readded.ReferenceID != added.ReferenceID {
				t.Fatal("group readd rebound reference", w.Code, w.Body.String())
			}
			// One reference and one explicitly saved leaf share the ordered
			// server page; the dynamic group's count includes the later member.
			leafPath := "/v1/items/" + items[0] + "/reading/my-list"
			leafSave := readingHTTP(t, h, "PUT", leafPath, token, "", ReadingSavedRequest{OperationID: "mixed-leaf-add", ExpectedRevision: 0, Listed: &yes})
			if leafSave.Code != 200 {
				t.Fatal("mixed leaf save", leafSave.Code, leafSave.Body.String())
			}
			type savedPage struct {
				Sections []struct {
					Entries []json.RawMessage `json:"entries"`
					Total   int               `json:"totalCount"`
					Next    string            `json:"nextCursor"`
				} `json:"sections"`
			}
			for _, direction := range []string{"asc", "desc"} {
				cursor := ""
				seen := map[string]bool{}
				for page := 0; page < 2; page++ {
					endpoint := "/v1/content?view=watchlist&shape=posters&limit=1&direction=" + direction
					if cursor != "" {
						endpoint += "&cursor=" + url.QueryEscape(cursor)
					}
					response := readingHTTP(t, h, "GET", endpoint, token, "", nil)
					var result savedPage
					if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &result) != nil || len(result.Sections) != 1 || result.Sections[0].Total != 2 || len(result.Sections[0].Entries) != 1 {
						t.Fatal("actual mixed Saved page", direction, page, response.Code, response.Body.String())
					}
					var card struct {
						ID, Kind     string
						Count        int64
						LibraryID    string
						ReadingGroup catalog.SavedReadingGroupLink
						Navigation   catalog.SavedReadingGroupNavigation
					}
					if json.Unmarshal(result.Sections[0].Entries[0], &card) != nil || seen[card.ID] {
						t.Fatal("duplicate/malformed mixed Saved card")
					}
					seen[card.ID] = true
					if card.Kind == "reading_group" && (card.ID != added.ReferenceID || card.Count != 3 || card.LibraryID != r.library || card.ReadingGroup.SeriesID != series || card.ReadingGroup.GroupKind != kind || card.Navigation.EntityID != series) {
						t.Fatal("dynamic group projection changed", string(result.Sections[0].Entries[0]))
					}
					cursor = result.Sections[0].Next
					if (page == 0) != (cursor != "") {
						t.Fatal("mixed cursor bound", page, cursor)
					}
				}
				if !seen[added.ReferenceID] || !seen[items[0]] {
					t.Fatal("mixed page omitted actual leaf/reference", seen)
				}
			}
			leafRemove := readingHTTP(t, h, "PUT", leafPath, token, "", ReadingSavedRequest{OperationID: "mixed-leaf-remove", ExpectedRevision: 1, Listed: &no})
			if leafRemove.Code != 200 {
				t.Fatal("mixed leaf cleanup", leafRemove.Code, leafRemove.Body.String())
			}
			privatePath := func(reference string) string { return "/v1/reading/saved-groups/" + reference }
			deleteBody := ReadingSavedGroupRemovalRequest{OperationID: "private-remove", ExpectedRevision: 3}
			foreign := readingHTTP(t, h, "DELETE", privatePath(added.ReferenceID), r.f.owner.AccessToken, "", deleteBody)
			absent := readingHTTP(t, h, "DELETE", privatePath(identity.Token()), r.f.owner.AccessToken, "", deleteBody)
			if foreign.Code != 404 || absent.Code != 404 || normalizedAnswer(foreign.Body.String(), added.ReferenceID) != normalizedAnswer(absent.Body.String(), "absent") {
				t.Fatal("foreign reference differs from absent", foreign.Code, foreign.Body.String(), absent.Code, absent.Body.String())
			}
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
				t.Fatal(err)
			}
			for _, method := range []string{"GET", "PUT"} {
				hidden := readingHTTP(t, h, method, path(series), token, "", add)
				missing := readingHTTP(t, h, method, path(strings.Repeat("a", 43)), token, "", add)
				if hidden.Code != 404 || missing.Code != 404 || normalizedAnswer(hidden.Body.String(), series) != normalizedAnswer(missing.Body.String(), strings.Repeat("a", 43)) {
					t.Fatal("hidden group differs from absent", method, hidden.Code, hidden.Body.String(), missing.Code, missing.Body.String())
				}
				for _, marker := range append([]string{name, series, r.library}, items...) {
					if strings.Contains(hidden.Body.String(), marker) {
						t.Fatal("hidden group metadata escaped", hidden.Body.String())
					}
				}
			}
			// The target is also unready. Reference-only removal must not ask
			// its group resolver to turn this into false empty/status data.
			if _, err = r.f.db.Exec(`UPDATE reading_attribution_state SET input_generation=input_generation+1,phase='scan' WHERE library_id=(SELECT id FROM catalog_libraries WHERE library_id=?)`, r.library); err != nil {
				t.Fatal(err)
			}
			// Reference-only removal remains possible while the target is hidden.
			w = readingHTTP(t, h, "DELETE", privatePath(added.ReferenceID), token, "", deleteBody)
			var deleted ReadingSavedGroupRemovalResponse
			if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &deleted) != nil || !deleted.Removed || deleted.Revision != 4 || deleted.ReferenceID != added.ReferenceID {
				t.Fatal("own hidden reference removal", w.Code, w.Body.String())
			}
			for _, marker := range append([]string{name, series, r.library}, items...) {
				if strings.Contains(w.Body.String(), marker) {
					t.Fatal("private removal disclosed target", w.Body.String())
				}
			}
			again := readingHTTP(t, h, "DELETE", privatePath(added.ReferenceID), token, "", deleteBody)
			var acknowledged ReadingSavedGroupRemovalResponse
			if again.Code != 200 || json.Unmarshal(again.Body.Bytes(), &acknowledged) != nil || acknowledged != deleted {
				t.Fatal("private removal receipt changed", again.Code, again.Body.String())
			}
			for _, item := range append(items, thirdItem) {
				state, err := r.f.cat.Personal(profile, item)
				if err != nil || state.Watchlisted {
					t.Fatal("private removal changed a leaf save", err, state)
				}
			}
			if err = retireProfileInHTTPTest(t, r.f.id, ctx, owner.AccountToken, child.ID, child.Revision, true); err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"reading_saved_groups", "reading_saved_group_receipts", "reading_saved_group_owners"} {
				var retained int
				if err = r.f.db.QueryRow(`SELECT count(*) FROM `+table+` WHERE owner_key=?`, profile).Scan(&retained); err != nil || retained != 0 {
					t.Fatal("profile erasure retained private Saved group data", table, retained, err)
				}
			}
			late := readingHTTP(t, h, "PUT", path(series), token, "", add)
			if late.Code == 200 {
				t.Fatal("retired profile recreated a private group reference", late.Body.String())
			}
		})
	}
}
