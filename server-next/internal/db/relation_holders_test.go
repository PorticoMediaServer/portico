package db_test

import (
	"errors"
	"path/filepath"
	"slices"
	"strconv"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A series of audiobooks and a library's collection hold their titles by relation. Like a show,
// each is visible only while it holds a title the profile may see (browse rules 1 and 2): in
// the list, in its total and opened directly, whether a rating limit or a blocked label hides
// its titles, and after a title is re-rated, taken away or given a label.
func TestSeriesAndCollectionsHoldNothingAProfileMayNotSee(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	books, err := d.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	films, err := d.CreateLibrary(ctx, "Films", db.LibraryMovies, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	seven, sixteen := 7, 16
	night, tides, mixed := db.ItemKey{Kind: db.KeyName, Key: "series|night"}, db.ItemKey{Kind: db.KeyName, Key: "series|tides"}, db.ItemKey{Kind: db.KeyName, Key: "series|mixed"}
	spooky, calm := db.ItemKey{Kind: db.KeyName, Key: "spooky"}, db.ItemKey{Kind: db.KeyName, Key: "calm"}
	title := func(kind db.ItemKind, name string, age *int, in db.ItemKey, relation db.RelationKind, labels ...string) db.ImportItem {
		it := db.ImportItem{Kind: kind, Keys: []db.ItemKey{{Kind: db.KeyName, Key: name}}, Title: name, RatingAge: age,
			ExtraOf: []db.ItemKey{in}, ExtraKind: relation}
		for _, l := range labels {
			it.Tags = append(it.Tags, db.TagName{Kind: db.TagLabel, Name: l})
		}
		return it
	}
	book := func(name string, age *int, in db.ItemKey, labels ...string) db.ImportItem {
		return title(db.KindAudiobook, name, age, in, db.RelationSeriesMember, labels...)
	}
	film := func(name string, age *int, in db.ItemKey, labels ...string) db.ImportItem {
		return title(db.KindMovie, name, age, in, db.RelationCollectionMember, labels...)
	}
	if _, err := d.Import(ctx, db.ImportBatch{LibraryID: books.ID, PathID: books.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindSeries, Title: "Night", Keys: []db.ItemKey{night}},
		{Kind: db.KindSeries, Title: "Tides", Keys: []db.ItemKey{tides}},
		{Kind: db.KindSeries, Title: "Mixed", Keys: []db.ItemKey{mixed}},
		book("Dark", &seven, night, "Scary"),
		book("Low Water", &seven, tides),
		book("High Water", &sixteen, tides),
		book("Quiet", nil, mixed),
		book("Loud", &sixteen, mixed),
	}}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Import(ctx, db.ImportBatch{LibraryID: films.ID, PathID: films.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: []db.ImportItem{
		{Kind: db.KindCollection, Title: "Spooky", Keys: []db.ItemKey{spooky}},
		{Kind: db.KindCollection, Title: "Calm", Keys: []db.ItemKey{calm}},
		film("Scream", &seven, spooky, "Scary"),
		film("Meadow", &seven, calm),
	}}); err != nil {
		t.Fatal(err)
	}
	labels, err := d.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v, %v", labels, err)
	}
	profile := func(p db.SeedProfile) db.Viewer {
		v, err := d.CreateSeedProfile(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	twelve := 12
	everyone := profile(db.SeedProfile{Name: "everyone"})
	timid := profile(db.SeedProfile{Name: "timid", BlockedTags: []string{labels[0].PublicID}})
	kid := profile(db.SeedProfile{Name: "kid", MaxRatingAge: &twelve})
	unrated := profile(db.SeedProfile{Name: "kid who may see unrated", MaxRatingAge: &twelve, AllowUnrated: true})

	ids := map[string]string{}
	for _, lib := range []db.Library{books, films} {
		for _, it := range everything(t, d, everyone, db.ItemQuery{Library: lib.PublicID}) {
			ids[it.Title] = it.PublicID
		}
	}
	// holders lists a library's series or collections for the profile, checks its total, and
	// checks that each one the profile is not shown also does not open, nor list its titles.
	holders := func(v db.Viewer, lib db.Library, kind db.ItemKind) (out []string) {
		t.Helper()
		q := db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}}
		for _, it := range everything(t, d, v, q) {
			out = append(out, it.Title)
		}
		q.WithTotal = true
		if page, err := d.Items(ctx, v, q); err != nil || page.Total != len(out) {
			t.Errorf("profile %d: total %d for %v (%v)", v.ProfileID, page.Total, out, err)
		}
		for _, name := range map[db.ItemKind][]string{db.KindSeries: {"Mixed", "Night", "Tides"}, db.KindCollection: {"Calm", "Spooky"}}[kind] {
			if slices.Contains(out, name) {
				continue
			}
			if _, err := d.Item(ctx, v, ids[name]); !errors.Is(err, db.ErrNotFound) {
				t.Errorf("profile %d opened %s, which it is not shown: %v", v.ProfileID, name, err)
			}
			list := db.ItemQuery{Series: ids[name]}
			if kind == db.KindCollection {
				list = db.ItemQuery{Collection: ids[name]}
			}
			if _, err := d.Items(ctx, v, list); !errors.Is(err, db.ErrNotFound) {
				t.Errorf("profile %d listed the titles of %s: %v", v.ProfileID, name, err)
			}
		}
		return out
	}
	flagsKept := func(when string) {
		t.Helper()
		if missing, _, _, err := db.HolderFlags(d); err != nil || len(missing) > 0 {
			t.Fatalf("%s: holders not flagged %v (%v)", when, missing, err)
		}
	}
	flagsKept("after the import")
	for _, c := range []struct {
		who           string
		v             db.Viewer
		series, colls []string
	}{
		{"everyone", everyone, []string{"Mixed", "Night", "Tides"}, []string{"Calm", "Spooky"}},
		// Night's one volume and Spooky's one film carry the label the profile is kept from.
		{"the profile kept from the label", timid, []string{"Mixed", "Tides"}, []string{"Calm"}},
		// Mixed holds an unrated volume and one past the limit.
		{"the rating-limited kid", kid, []string{"Night", "Tides"}, []string{"Calm", "Spooky"}},
		{"the kid allowed unrated titles", unrated, []string{"Mixed", "Night", "Tides"}, []string{"Calm", "Spooky"}},
	} {
		if got := holders(c.v, books, db.KindSeries); !slices.Equal(got, c.series) {
			t.Errorf("%s sees series %v, want %v", c.who, got, c.series)
		}
		if got := holders(c.v, films, db.KindCollection); !slices.Equal(got, c.colls) {
			t.Errorf("%s sees collections %v, want %v", c.who, got, c.colls)
		}
	}

	// Tides' one volume the kid may see is re-rated past the limit, then back.
	if err := db.SetRatingAge(d, ids["Low Water"], 16); err != nil {
		t.Fatal(err)
	}
	flagsKept("after a volume was re-rated")
	if got := holders(kid, books, db.KindSeries); !slices.Equal(got, []string{"Night"}) {
		t.Errorf("after Low Water was re-rated 16 the kid sees %v", got)
	}
	if err := db.SetRatingAge(d, ids["Low Water"], 7); err != nil {
		t.Fatal(err)
	}
	if got := holders(kid, books, db.KindSeries); !slices.Equal(got, []string{"Night", "Tides"}) {
		t.Errorf("after Low Water was re-rated 7 the kid sees %v", got)
	}
	// Then taken away: Tides holds only the volume past the limit.
	if err := db.RemoveItem(d, ids["Low Water"]); err != nil {
		t.Fatal(err)
	}
	flagsKept("after a volume was removed")
	if got := holders(kid, books, db.KindSeries); !slices.Equal(got, []string{"Night"}) {
		t.Errorf("after Low Water was removed the kid sees %v", got)
	}
	if got := holders(everyone, books, db.KindSeries); !slices.Equal(got, []string{"Mixed", "Night", "Tides"}) {
		t.Errorf("after Low Water was removed everyone sees %v", got)
	}
	// Calm's one film is given the label afterwards.
	if err := db.PutLabel(d, ids["Meadow"], "Scary", false); err != nil {
		t.Fatal(err)
	}
	flagsKept("after a film was given a label")
	if got := holders(timid, films, db.KindCollection); len(got) != 0 {
		t.Errorf("after Meadow was labelled the profile kept from the label sees %v", got)
	}
}

// A list of one show, one person or one collection leaves out a season or show the profile is
// left with nothing in, and one a blocked label covers, as a library's list does, and counts
// both out of the total.
func TestAListOfOneTitlePersonOrCollectionLeavesOutEmptiedHolders(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	shows, err := d.CreateLibrary(ctx, "Shows", db.LibraryShows, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	one, two := 1, 2
	box := db.ItemKey{Kind: db.KeyName, Key: "box"}
	host := []db.ImportCredit{{Name: "Pat Host", Role: db.RoleActor}}
	var items []db.ImportItem
	show := func(name string, seasons ...bool) { // each season: whether its one episode carries the label
		key := db.ItemKey{Kind: db.KeyName, Key: name}
		items = append(items, db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{key}, Title: name, Credits: host,
			ExtraOf: []db.ItemKey{box}, ExtraKind: db.RelationCollectionMember})
		for n, scary := range seasons {
			number := []*int{&one, &two}[n]
			season := db.ItemKey{Kind: db.KeyName, Key: name + "/" + itoa(n)}
			episode := db.ImportItem{Kind: db.KindEpisode, Keys: []db.ItemKey{{Kind: db.KeyName, Key: name + "/" + itoa(n) + "/1"}}, Parent: &season,
				Title: name + " episode", IndexNumber: &one}
			if scary {
				episode.Tags = []db.TagName{{Kind: db.TagLabel, Name: "Scary"}}
			}
			items = append(items, db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{season}, Parent: &key, Title: "Season " + itoa(n+1), IndexNumber: number}, episode)
		}
	}
	items = append(items, db.ImportItem{Kind: db.KindCollection, Title: "Box", Keys: []db.ItemKey{box}})
	show("Haunted", true)
	show("Sunny", false, true)
	// And rows the label covers themselves: a third season of Sunny, and a show in the box.
	three, sunny, locked := 3, db.ItemKey{Kind: db.KeyName, Key: "Sunny"}, db.ItemKey{Kind: db.KeyName, Key: "Locked"}
	scary := []db.TagName{{Kind: db.TagLabel, Name: "Scary"}}
	items = append(items,
		db.ImportItem{Kind: db.KindSeason, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "Sunny/2"}}, Parent: &sunny, Title: "Season 3", IndexNumber: &three, Tags: scary},
		db.ImportItem{Kind: db.KindShow, Keys: []db.ItemKey{locked}, Title: "Locked", Credits: host, Tags: scary,
			ExtraOf: []db.ItemKey{box}, ExtraKind: db.RelationCollectionMember})
	if _, err := d.Import(ctx, db.ImportBatch{LibraryID: shows.ID, PathID: shows.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Items: items}); err != nil {
		t.Fatal(err)
	}
	labels, err := d.Tags(ctx, db.TagLabel)
	if err != nil || len(labels) != 1 {
		t.Fatalf("labels: %v, %v", labels, err)
	}
	everyone, err := d.CreateSeedProfile(ctx, db.SeedProfile{Name: "everyone"})
	if err != nil {
		t.Fatal(err)
	}
	timid, err := d.CreateSeedProfile(ctx, db.SeedProfile{Name: "timid", BlockedTags: []string{labels[0].PublicID}})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, it := range everything(t, d, everyone, db.ItemQuery{Library: shows.PublicID, Kinds: []db.ItemKind{db.KindShow, db.KindCollection}}) {
		ids[it.Title] = it.PublicID
	}
	detail, err := d.Item(ctx, everyone, ids["Sunny"])
	if err != nil || len(detail.Credits) == 0 {
		t.Fatalf("Sunny: %+v, %v", detail.Credits, err)
	}
	for _, c := range []struct {
		name string
		q    db.ItemQuery
		want []string
	}{
		{"Sunny's seasons", db.ItemQuery{Parent: ids["Sunny"]}, []string{"Season 1"}},
		{"Pat Host's titles", db.ItemQuery{Person: detail.Credits[0].PersonID}, []string{"Sunny"}},
		{"the Box collection", db.ItemQuery{Collection: ids["Box"]}, []string{"Sunny"}},
	} {
		var got []string
		for _, it := range everything(t, d, timid, c.q) {
			got = append(got, it.Title)
		}
		c.q.WithTotal = true
		page, err := d.Items(ctx, timid, c.q)
		if !slices.Equal(got, c.want) || err != nil || page.Total != len(c.want) {
			t.Errorf("%s for the profile kept from the label: %v, total %d (%v); want %v", c.name, got, page.Total, err, c.want)
		}
		if all := everything(t, d, everyone, c.q); len(all) != 3 {
			t.Errorf("%s for everyone: %d, want 3", c.name, len(all))
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }
