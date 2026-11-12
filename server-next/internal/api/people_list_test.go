package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An audiobook library's Authors and Series tabs, and the series page. The library holds three
// books by Ada Adams and Ben Brook rated 7, and two rated 16: volume 2 of Ada Adams's series
// "Tides", and Cy Cole's "Thunder", the only volume of "Storm".
// A profile limited to 12 sees no trace of the rated book: not in the series, not in an
// author's count; and an author or series left with nothing it may see is not listed.
func TestAnAudiobookLibrarysAuthorsAndSeries(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	by := func(name string) []db.ImportCredit {
		return []db.ImportCredit{{Name: name, Role: db.RoleAuthor}, {Name: "Nora Reader", Role: db.RoleNarrator}}
	}
	series := db.ItemKey{Kind: db.KeyName, Key: "series|tides"}
	one, two, seven, sixteen := 1, 2, 7, 16
	book := func(title, file string, credits []db.ImportCredit, volume *int, age *int, in *db.ItemKey) db.ImportItem {
		it := db.ImportItem{Kind: db.KindAudiobook, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|" + title}}, Title: title,
			Credits: credits, IndexNumber: volume, RatingAge: age,
			Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: db.FileRef{Name: file}}}}}}
		if in != nil {
			it.ExtraOf, it.ExtraKind = []db.ItemKey{*in}, db.RelationSeriesMember
		}
		return it
	}
	var files []db.ImportFile
	for _, name := range []string{"a.m4b", "b.m4b", "c.m4b", "d.m4b"} {
		files = append(files, db.ImportFile{FileRef: db.FileRef{Name: name}, Role: db.FileMedia, Size: 1, ModifiedNS: 1, Container: "mp4", DurationMS: 60_000,
			Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "aac", Channels: 2, SampleRate: 44100}}})
	}
	storm := db.ItemKey{Kind: db.KeyName, Key: "series|storm"}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Files: files,
		Items: []db.ImportItem{
			{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{series}},
			{Kind: db.KindSeries, Title: "Storm", Keys: []db.ItemKey{storm}},
			book("High Water", "b.m4b", by("Ada Adams"), &two, &sixteen, &series),
			book("Low Water", "a.m4b", by("Ada Adams"), &one, &seven, &series),
			book("Alone", "c.m4b", by("Ben Brook"), nil, &seven, nil),
			book("Thunder", "d.m4b", []db.ImportCredit{{Name: "Cy Cole", Role: db.RoleAuthor}}, &one, &sixteen, &storm),
		}}); err != nil {
		t.Fatal(err)
	}
	var kid Profile
	call(t, s, "POST", "/v1/profiles", owner, ProfileCreate{Name: "Kid"}, &kid)
	limit := 12
	if w := call(t, s, "PUT", "/v1/profiles/"+kid.ID+"/restrictions", owner, Restrictions{MaxRatingAge: &limit, AllLibraries: true}, nil); w.Code != 200 {
		t.Fatalf("limiting the kid answered %d: %s", w.Code, w.Body)
	}
	var onKid Session
	if w := call(t, s, "POST", "/v1/profiles/"+kid.ID+"/select", signIn(t, s, 1).Session.AccessToken, ProfileSelect{}, &onKid); w.Code != 200 {
		t.Fatalf("choosing the kid's profile answered %d", w.Code)
	}

	// The library offers the Series tab.
	if views := get[Library](t, s, owner, "/v1/libraries/"+lib.PublicID).Views; !slices.Contains(views, View{"series"}) {
		t.Errorf("the audiobook library's views are %v, without series", views)
	}
	people := func(token, query string) (names []string, counts []int, page PersonPage) {
		t.Helper()
		page = get[PersonPage](t, s, token, "/v1/people?library="+lib.PublicID+query)
		for _, p := range page.Items {
			names, counts = append(names, p.Name), append(counts, *p.TitleCount)
		}
		return
	}
	titles := func(token, path string) (out []string) {
		t.Helper()
		for _, it := range get[ItemPage](t, s, token, path).Items {
			out = append(out, it.Title)
		}
		return
	}

	// Authors, by name, each with their titles in this library; a page at a time.
	names, counts, page := people(owner, "&role=author&total=true")
	if !slices.Equal(names, []string{"Ada Adams", "Ben Brook", "Cy Cole"}) || !slices.Equal(counts, []int{2, 1, 1}) || page.Total == nil || *page.Total != 3 {
		t.Fatalf("the owner's authors: %v %v total %v", names, counts, page.Total)
	}
	first, _, page := people(owner, "&role=author&limit=2")
	rest, _, last := people(owner, "&role=author&limit=2&cursor="+page.NextCursor)
	if !slices.Equal(first, []string{"Ada Adams", "Ben Brook"}) || !slices.Equal(rest, []string{"Cy Cole"}) || last.NextCursor != "" {
		t.Errorf("the authors two at a time: %v then %v, next %q", first, rest, last.NextCursor)
	}
	if names, counts, _ := people(owner, "&role=narrator"); !slices.Equal(names, []string{"Nora Reader"}) || counts[0] != 3 {
		t.Errorf("the narrators: %v %v", names, counts)
	}
	// An author's page lists their books in this library.
	ada := page.Items[0].ID
	if got := titles(owner, "/v1/items?library="+lib.PublicID+"&author="+ada); !slices.Equal(got, []string{"High Water", "Low Water"}) {
		t.Errorf("Ada Adams's books: %v", got)
	}
	// The kid: the rated book is in no count, and the author with nothing else is not there.
	names, counts, page = people(onKid.AccessToken, "&role=author&total=true")
	if !slices.Equal(names, []string{"Ada Adams", "Ben Brook"}) || !slices.Equal(counts, []int{1, 1}) || *page.Total != 2 {
		t.Errorf("the kid's authors: %v %v total %d", names, counts, *page.Total)
	}

	// Series: the tab, then one series in its own order, and sorted another way.
	all := get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID+"&kind=series")
	if len(all.Items) != 2 || all.Items[1].Title != "Tides" {
		t.Fatalf("the owner's series: %+v", all.Items)
	}
	tides := all.Items[1].ID
	if got := titles(owner, "/v1/items?series="+tides); !slices.Equal(got, []string{"Low Water", "High Water"}) {
		t.Errorf("Tides in its own order: %v", got)
	}
	if got := titles(owner, "/v1/items?series="+tides+"&sort=title"); !slices.Equal(got, []string{"High Water", "Low Water"}) {
		t.Errorf("Tides by title: %v", got)
	}
	if got := titles(onKid.AccessToken, "/v1/items?library="+lib.PublicID+"&kind=series"); !slices.Equal(got, []string{"Tides"}) {
		t.Errorf("the kid's series (Storm holds nothing the kid may see): %v", got)
	}
	if got := titles(onKid.AccessToken, "/v1/items?series="+tides); !slices.Equal(got, []string{"Low Water"}) {
		t.Errorf("Tides for the kid: %v", got)
	}
	if code := status(t, s, "GET", "/v1/items?series="+all.Items[0].ID, onKid.AccessToken, nil); code != 404 {
		t.Errorf("a series the kid may see nothing of answered %d, want 404", code)
	}
	// Refused plainly: no library, a role that is not listed this way, another library's kind of id.
	for _, query := range []string{"/v1/people", "/v1/people?library=" + lib.PublicID + "&role=actor", "/v1/people?library=nope&role=author"} {
		if code := status(t, s, "GET", query, owner, nil); code != 400 && code != 404 {
			t.Errorf("%s answered %d", query, code)
		}
	}
}
