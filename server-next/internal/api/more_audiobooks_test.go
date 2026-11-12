package api

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An audiobook's page ends with More audiobooks: the library's other audiobooks the profile
// may see, newest added first, a finished one among them, never the book itself nor the other
// books of its own series.
func TestAnAudiobooksPageHasMoreAudiobooks(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	tides := db.ItemKey{Kind: db.KeyName, Key: "series|tides"}
	day := int64(24 * time.Hour / time.Millisecond)
	start := time.Now().UnixMilli() - 30*day
	book := func(title string, age int64, in *db.ItemKey, labels ...string) db.ImportItem {
		it := db.ImportItem{Kind: db.KindAudiobook, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|" + title}}, Title: title,
			AddedAt: start + age*day, DurationMS: 60_000}
		if in != nil {
			it.ExtraOf, it.ExtraKind = []db.ItemKey{*in}, db.RelationSeriesMember
		}
		for _, l := range labels {
			it.Tags = append(it.Tags, db.TagName{Kind: db.TagLabel, Name: l})
		}
		return it
	}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{tides}},
		book("High Water", 1, &tides),
		book("Low Water", 9, &tides),
		book("Oldest", 0, nil),
		book("Finished", 5, nil),
		book("Newest", 8, nil),
		book("Secret", 7, nil, "Hush"),
	}}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=audiobook").Items {
		ids[it.Title] = it.ID
	}
	yes := true
	if w := call(t, s, "PUT", "/v1/items/"+ids["Finished"]+"/state", owner, map[string]any{"watched": yes}, nil); w.Code != 200 {
		t.Fatalf("marking Finished answered %d: %s", w.Code, w.Body)
	}
	more := func(token, title string) (out []string) {
		t.Helper()
		for _, g := range get[Related](t, s, token, "/v1/items/"+ids[title]+"/related").Groups {
			if g.Relation == "more_audiobooks" {
				for _, it := range g.Items {
					out = append(out, it.Title)
					if token == owner && it.Title == "Finished" && !it.State.Watched {
						t.Errorf("the finished book is offered without its state")
					}
				}
			}
		}
		return out
	}
	if got := more(owner, "High Water"); !slices.Equal(got, []string{"Newest", "Secret", "Finished", "Oldest"}) {
		t.Errorf("More audiobooks on High Water: %v", got)
	}
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
	if got := more(quiet.AccessToken, "Oldest"); !slices.Equal(got, []string{"Low Water", "Newest", "Finished", "High Water"}) {
		t.Errorf("More audiobooks on Oldest for the profile kept from the label: %v", got)
	}
}
