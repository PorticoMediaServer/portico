package api

import (
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An audiobook's own page names the series it is a volume of, to link to it; a book in no
// series names none, and a profile kept from the series is not told of it (browse rules 1, 2).
func TestABooksPageNamesItsSeries(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tides, two := db.ItemKey{Kind: db.KeyName, Key: "series|tides"}, 2
	book := func(title string, in *db.ItemKey) db.ImportItem {
		it := db.ImportItem{Kind: db.KindAudiobook, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|" + title}}, Title: title, IndexNumber: &two}
		if in != nil {
			it.ExtraOf, it.ExtraKind = []db.ItemKey{*in}, db.RelationSeriesMember
		}
		return it
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{tides}, Tags: []db.TagName{{Kind: db.TagLabel, Name: "Sea"}}},
		book("High Water", &tides),
		book("Alone", nil),
	}}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=audiobook,series").Items {
		ids[it.Title] = it.ID
	}
	if got := get[Item](t, s, owner, "/v1/items/"+ids["High Water"]); got.Series == nil || got.Series.ID != ids["Tides"] ||
		got.Series.Kind != "series" || got.Series.Title != "Tides" || got.Index == nil || *got.Index != 2 {
		t.Errorf("volume 2 of Tides names its series as %+v, index %v", got.Series, got.Index)
	}
	if got := get[Item](t, s, owner, "/v1/items/"+ids["Alone"]); got.Series != nil {
		t.Errorf("a book in no series names %+v", got.Series)
	}
	// The label is on the series alone, so the profile kept from it still sees the book.
	labels, err := s.db.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v %v", labels, err)
	}
	var p Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Quiet"}, &p)
	if w := call(t, s, "PUT", "/v1/profiles/"+p.ID+"/restrictions", owner, Restrictions{AllLibraries: true, BlockedLabels: []string{labels[0].PublicID}}, nil); w.Code != 200 {
		t.Fatalf("restricting answered %d: %s", w.Code, w.Body)
	}
	var quiet Session
	if w := call(t, s, "POST", "/v1/profiles/"+p.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &quiet); w.Code != 200 {
		t.Fatalf("choosing the profile answered %d", w.Code)
	}
	if got := get[Item](t, s, quiet.AccessToken, "/v1/items/"+ids["High Water"]); got.Series != nil {
		t.Errorf("a profile kept from the series is told of it: %+v", got.Series)
	}
}
