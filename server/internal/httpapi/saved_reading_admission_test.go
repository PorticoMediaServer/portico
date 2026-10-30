package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Real Reading items keep their own identities and use personal Saved
// membership. Opening a source or receiving an AV/job capability is unnecessary.
func TestSavedCollectionAdmitsReadingAndProjectsReadingFacts(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, format := range []string{"epub", "cbz"} {
		t.Run(format, func(t *testing.T) {
			kind := "book"
			if format == "cbz" {
				kind = "comic"
			}
			r := newReadingFixture(t, kind, format)
			// The scan only admits the file. The real reading producer adopts
			// its author; the collection read below still opens no source.
			readingPublishFileFacts(t, r)
			h := New(r.d)
			token := r.f.owner.AccessToken
			var created struct{ Receipt catalog.SavedResourceReceipt }
			w := readingHTTP(t, h, "POST", "/v1/saved-resources", token, "", map[string]any{"operationId": "reading-collection-create", "expectedRevision": 0, "kind": "collection", "name": "Reading picks"})
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &created) != nil || created.Receipt.ResourceID == "" {
				t.Fatal("create personal collection", w.Code)
			}
			path := "/v1/saved-resources/" + created.Receipt.ResourceID
			body := map[string]any{"operationId": "reading-collection-add", "expectedRevision": created.Receipt.Revision, "addItemIds": []string{r.item}}
			var added struct{ Receipt catalog.SavedResourceReceipt }
			w = readingHTTP(t, h, "PUT", path+"/entries", token, "", body)
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &added) != nil || added.Receipt.Entries == nil || len(added.Receipt.Entries.Added) != 1 || len(added.Receipt.Entries.Failed) != 0 {
				t.Fatal("Reading membership refused", w.Code)
			}
			var replay struct{ Receipt catalog.SavedResourceReceipt }
			w = readingHTTP(t, h, "PUT", path+"/entries", token, "", body)
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &replay) != nil || replay.Receipt.Revision != added.Receipt.Revision {
				t.Fatal("membership retry did not resolve the committed receipt", w.Code)
			}
			var page catalog.SavedResourceContent
			w = readingHTTP(t, h, "GET", path+"/content", token, "", nil)
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &page) != nil || len(page.Entries) != 1 || page.Entries[0].Hidden || page.Entries[0].Media == nil {
				t.Fatal("collection did not project its real Reading member", w.Code)
			}
			entry := page.Entries[0].Media
			if entry.ID != r.item || entry.LibraryID != r.library || entry.Author != r.fileAuthor || entry.Playback != nil || entry.Duration != nil || entry.ProgressSeconds != nil {
				t.Fatal("Reading identity/facts replaced by timed-media facts")
			}
			actions := entry.HomeActions
			if actions == nil || !actions.Collection || !actions.Selection.Collection || actions.Play || actions.Queue || actions.Playlist || actions.Selection.Play || actions.Selection.Queue || actions.Selection.Playlist || actions.Selection.Edit || actions.Selection.Refresh {
				t.Fatal("Reading received an unsupported action")
			}
			var detail catalog.Detail
			w = readingHTTP(t, h, "GET", "/v1/items/"+r.item+"/detail", token, "", nil)
			if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &detail) != nil || detail.Item.ID != r.item || detail.Item.Kind != entry.Kind || detail.Scope.LibraryID != r.library || len(detail.Actions) != 1 || detail.Actions[0].ID != "add_to_collection" || !detail.Actions[0].Enabled {
				t.Fatal("Reading detail did not publish its exact Collection action", w.Code)
			}
			// Admission was successful above; retirement must be observed again
			// by the callback inside the accepting write, using the old principal.
			ctx := context.Background()
			if err := r.d.savedResourceItemAccess(ctx, r.p, r.item); err != nil {
				t.Fatal("initial admission", err)
			}
			if _, err := dbwork.ExecWrite(ctx, r.f.db, dbwork.ClassInteractive, `UPDATE catalog_entities SET retired=1 WHERE public_id=pid_blob(?)`, r.item); err != nil {
				t.Fatal(err)
			}
			gate, err := dbwork.Begin(ctx, r.f.db, dbwork.ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer gate.Rollback()
			if err := r.d.savedResourceItemAuthorityTx(ctx, gate.Tx(), r.p, r.item); err == nil {
				t.Fatal("retired Reading target retained a saved-entry grant")
			}
		})
	}
}

// The detail route's Reading fallback is the exact personal authority, not a
// wider grant: the profile's restrictions, library grant and live entity facts
// still decide the leaf, and a refusal keeps the timed-media lookup's concealed
// absence. Only kinds 20/21 take the fallback at all.
func TestReadingDetailFallbackUsesReadingPersonalFences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, format := range []string{"epub", "cbz"} {
		t.Run(format, func(t *testing.T) {
			kind := "book"
			if format == "cbz" {
				kind = "comic"
			}
			r := newReadingFixture(t, kind, format)
			readingPublishFileFacts(t, r)
			h := New(r.d)
			ctx := context.Background()
			var stored int
			if err := r.f.db.QueryRow(`SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, r.item).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if stored != 20 && stored != 21 {
				t.Fatalf("fixture item kind %d is not a Reading leaf", stored)
			}
			// The timed-media lookup conceals this leaf, so a 200 below is the
			// Reading fallback's own decision and not the ordinary path.
			if _, err := r.f.cat.LibraryForItem(r.item); !errors.Is(err, sql.ErrNoRows) {
				t.Fatalf("timed-media lookup did not conceal the Reading leaf: %v", err)
			}
			owner := readingOwnerLogin(t, r)
			created, err := r.f.id.CreateDirectProfile(ctx, owner.AccountToken, "Detail reader", "mint")
			if err != nil {
				t.Fatal(err)
			}
			var child identity.DirectProfile
			for _, p := range created.Profiles {
				if !p.Primary && p.Name == "Detail reader" {
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
			detail := func(id string) (int, string) {
				w := readingHTTP(t, h, "GET", "/v1/items/"+id+"/detail", token, "", nil)
				return w.Code, w.Body.String()
			}
			if status, body := detail(r.item); status != http.StatusOK {
				t.Fatalf("Reading detail refused its own personal authority: %d %s", status, body)
			}
			refuse := func() {
				t.Helper()
				hiddenStatus, hiddenBody := detail(r.item)
				absentStatus, absentBody := detail(absentTwin(r.item))
				if hiddenStatus != http.StatusNotFound || absentStatus != http.StatusNotFound || normalizedAnswer(hiddenBody, r.item) != normalizedAnswer(absentBody, absentTwin(r.item)) {
					t.Fatalf("hidden/absent Reading detail differs: %d %s, %d %s", hiddenStatus, hiddenBody, absentStatus, absentBody)
				}
				for _, marker := range []string{r.item, r.source, r.library, r.path} {
					if marker != "" && strings.Contains(hiddenBody, marker) {
						t.Fatalf("a refused Reading detail leaked %q: %s", marker, hiddenBody)
					}
				}
			}
			childRevision := child.Revision
			edit := func(change identity.DirectProfileEdit) {
				t.Helper()
				change.ExpectedRevision = childRevision
				snapshot, err := r.f.id.EditDirectProfile(ctx, owner.AccountToken, child.ID, change)
				if err != nil {
					t.Fatal(err)
				}
				for _, p := range snapshot.Profiles {
					if p.ID == child.ID {
						childRevision = p.Revision
						return
					}
				}
				t.Fatal("child profile missing")
			}
			// The profile's own restriction, not the concealed timed-media kind.
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 1, AllowUnrated: false}); err != nil {
				t.Fatal(err)
			}
			refuse()
			if _, err = r.f.id.SetProfileRestrictions(ctx, owner.AccountToken, child.ID, identity.ProfileRestrictionEdit{ExpectedRevision: 2, AllowUnrated: true}); err != nil {
				t.Fatal(err)
			}
			if status, body := detail(r.item); status != http.StatusOK {
				t.Fatalf("restored restriction still refused: %d %s", status, body)
			}
			// The profile's library grant is the same fence the library's own
			// routes use; losing it conceals the leaf again.
			edit(identity.DirectProfileEdit{Policy: &identity.ProfilePolicy{AllowedLibraries: []string{}}})
			refuse()
			edit(identity.DirectProfileEdit{Policy: &identity.ProfilePolicy{AllowedLibraries: []string{r.library}}})
			if status, body := detail(r.item); status != http.StatusOK {
				t.Fatalf("restored library still refused: %d %s", status, body)
			}
		})
	}
}
