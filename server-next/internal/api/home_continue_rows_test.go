package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// Home's Continue rows (definition area 5, rules 37 and 38; decision Q24): a film in progress
// is in Continue Watching, a song in Continue Listening, and an audiobook, like a book, in
// Continue your books; none of them is in another's row. The books row cannot be hidden.
func TestHomeHasAContinueRowForWatchingListeningAndBooks(t *testing.T) {
	th := newTheatre(t)
	ctx := context.Background()
	owner := db.Viewer{UserID: 1, ProfileID: 1}
	started := map[db.ItemKind]string{}
	for kind, item := range map[string]db.ItemKind{db.LibraryMovies: db.KindMovie, db.LibraryMusic: db.KindTrack,
		db.LibraryAudiobooks: db.KindAudiobook} {
		lib, err := th.s.db.CreateLibrary(ctx, kind, kind, "en", []string{"/media/" + kind})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := th.s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
			Files: []db.ImportFile{{FileRef: db.FileRef{Name: "one.file"}, Role: db.FileMedia, Size: 1 << 20}},
			Items: []db.ImportItem{{Kind: item, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: "one"}}, Title: "One " + kind,
				Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: "one.file"}}}}}}}}); err != nil {
			t.Fatal(err)
		}
		page, err := th.s.db.Items(ctx, owner, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{item}, Sort: db.SortTitle})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("%s: %d items, %v", kind, len(page.Items), err)
		}
		at := int64(60_000)
		if _, _, err := th.s.db.SetState(ctx, owner, page.Items[0].PublicID, db.StateEdit{PositionMS: &at}); err != nil {
			t.Fatal(err)
		}
		started[item] = page.Items[0].PublicID
	}
	want := map[string][]string{"continue": {started[db.KindMovie]}, "continue_listening": {started[db.KindTrack]},
		"continue_reading": {started[db.KindAudiobook]}}
	home := func() map[string][]string {
		got := map[string][]string{}
		for _, row := range get[Home](t, th.s, th.owner.AccessToken, "/v1/home").Rows {
			for _, item := range row.Items {
				got[row.ID] = append(got[row.ID], item.ID)
			}
		}
		return got
	}
	got := home()
	for row, ids := range want {
		if !slices.Equal(got[row], ids) {
			t.Errorf("Home row %s holds %v; want %v (all rows: %v)", row, got[row], ids, got)
		}
	}
	body := PreferencesEdit{Scope: "profile-server", DeviceClass: "web", Values: map[string]any{
		"home.hiddenRowIds": []string{"continue", "continue_listening", "continue_reading"}}}
	if w := call(t, th.s, "PATCH", "/v1/preferences", th.owner.AccessToken, body, nil); w.Code != 200 {
		t.Fatalf("saving the layout answered %d: %s", w.Code, w.Body)
	}
	if got := home(); len(got["continue"]) != 1 || len(got["continue_listening"]) != 1 || len(got["continue_reading"]) != 1 {
		t.Errorf("a Continue row was hidden: %v", got)
	}
}
