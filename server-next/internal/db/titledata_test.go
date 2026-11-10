package db_test

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/titledata/titledatatest"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// titles answers the first n titles of a kind across the seeded libraries, with their ids.
func titles(t *testing.T, l *library, kind db.ItemKind, n int) []db.ItemDetail {
	t.Helper()
	var out []db.ItemDetail
	for _, lib := range l.libs {
		if seed.TitleKind(lib.Kind) != kind {
			continue
		}
		page, err := l.d.Items(ctx, l.all, db.ItemQuery{Library: lib.PublicID, Kinds: []db.ItemKind{kind}, Limit: n})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			detail, err := l.d.Item(ctx, l.all, it.PublicID)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
	}
	if len(out) < n {
		t.Fatalf("the seed has %d titles of kind %s, want %d", len(out), kind, n)
	}
	return out[:n]
}

func externalID(t *testing.T, it db.ItemDetail, provider string) string {
	t.Helper()
	for _, id := range it.ExternalIDs {
		if id.Provider == provider {
			return id.ID
		}
	}
	t.Fatalf("%q has no %s id", it.Title, provider)
	return ""
}

// useTitleData writes f, prepares it as the server would after checking its signature, and
// puts it in use.
func useTitleData(t *testing.T, d *db.DB, f titledatatest.File) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.sqlite")
	titledatatest.Write(t, path, f)
	if f.Version == "" {
		f.Version = "2026.45"
	}
	if err := db.PrepareTitleData(ctx, path, "screen", f.Version); err != nil {
		t.Fatal(err)
	}
	if err := d.UseTitleData(path); err != nil {
		t.Fatal(err)
	}
}

func applyTitleData(t *testing.T, d *db.DB) {
	t.Helper()
	for after := int64(0); ; {
		next, err := d.ApplyTitleData(ctx, after, 7)
		if err != nil {
			t.Fatal(err)
		}
		if next == 0 {
			break
		}
		after = next
	}
	if err := d.RemoveUnusedTitleDataTags(ctx); err != nil {
		t.Fatal(err)
	}
}

// moodTags answers each mood tag in the database with the titles carrying it.
func moodTags(t *testing.T, l *library) map[string][]string {
	t.Helper()
	tags, err := l.d.Tags(ctx, db.TagMood)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	for _, tag := range tags {
		page, err := l.d.Items(ctx, l.all, db.ItemQuery{Tag: tag.PublicID, Kinds: []db.ItemKind{db.KindMovie, db.KindShow}, Limit: 100})
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range page.Items {
			out[tag.Name] = append(out[tag.Name], it.PublicID)
		}
		slices.Sort(out[tag.Name])
	}
	return out
}

func sorted(ids ...string) []string {
	slices.Sort(ids)
	return ids
}

// Films and shows get the title data's row-making tags of strength 2 or 3, matched by the ids
// their providers gave them. A new file replaces what an older one gave, a title that no longer
// matches loses its tags, the tags show on no title's page, and with no file every one goes.
func TestMoodTagsFollowTheTitleDataByProviderID(t *testing.T) {
	l := newLibrary(t, 600)
	films, shows := titles(t, l, db.KindMovie, 3), titles(t, l, db.KindShow, 1)
	vocabulary := []titledatatest.Tag{{ID: 1, Key: "mood:cozy", RowLabel: "cozy stories"},
		{ID: 2, Key: "subgenre:folk-horror", RowLabel: "folk horror"}, {ID: 3, Key: "intensity:violence"},
		{ID: 4, Key: "mood:gloomy", RowLabel: "gloomy films", Retired: true}}
	tmdb := func(n int) map[int]string {
		return map[int]string{titledatatest.TMDBMovie: externalID(t, films[n], "tmdb")}
	}
	useTitleData(t, l.d, titledatatest.File{Vocabulary: vocabulary, Works: []titledatatest.Work{
		{QID: 10, IDs: tmdb(0), Tags: map[int]int{1: 3, 3: 3, 4: 3}},
		{QID: 11, IDs: tmdb(1), Tags: map[int]int{1: 1, 2: 2}},
		// A TV id equal to a film's TMDB id is another title.
		{QID: 12, Show: true, IDs: map[int]string{titledatatest.TMDBShow: externalID(t, films[2], "tmdb")}, Tags: map[int]int{1: 3}},
		{QID: 13, Show: true, IDs: map[int]string{titledatatest.TVDBSeries: externalID(t, shows[0], "tvdb")}, Tags: map[int]int{1: 2}},
	}})
	applyTitleData(t, l.d)
	want := map[string][]string{
		"mood:cozy":            sorted(films[0].PublicID, shows[0].PublicID),
		"subgenre:folk-horror": {films[1].PublicID},
	}
	if got := moodTags(t, l); !maps(got, want) {
		t.Fatalf("after the first file the mood tags are %v, want %v", got, want)
	}
	detail, err := l.d.Item(ctx, l.all, films[0].PublicID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range detail.Tags {
		if tag.Kind == db.TagMood {
			t.Fatalf("a title's page shows the mood tag %q", tag.Name)
		}
	}

	// The next file no longer knows the first film and changes the second's tag.
	useTitleData(t, l.d, titledatatest.File{Version: "2026.46", Vocabulary: vocabulary, Works: []titledatatest.Work{
		{QID: 11, IDs: tmdb(1), Tags: map[int]int{1: 2}},
		{QID: 13, Show: true, IDs: map[int]string{titledatatest.TVDBSeries: externalID(t, shows[0], "tvdb")}, Tags: map[int]int{1: 2}},
	}})
	applyTitleData(t, l.d)
	applyTitleData(t, l.d) // a second pass changes nothing
	want = map[string][]string{"mood:cozy": sorted(films[1].PublicID, shows[0].PublicID)}
	if got := moodTags(t, l); !maps(got, want) {
		t.Fatalf("after the second file the mood tags are %v, want %v", got, want)
	}

	if err := l.d.UseTitleData(""); err != nil {
		t.Fatal(err)
	}
	for left := true; left; {
		if left, err = l.d.RemoveTitleDataTags(ctx, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.d.RemoveUnusedTitleDataTags(ctx); err != nil {
		t.Fatal(err)
	}
	if got := moodTags(t, l); len(got) != 0 {
		t.Fatalf("with title data switched off the mood tags are %v", got)
	}
}

func maps(a, b map[string][]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if !slices.Equal(v, b[k]) {
			return false
		}
	}
	return true
}
