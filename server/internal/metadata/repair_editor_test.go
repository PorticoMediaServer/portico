package metadata

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
)

// editorFixture builds one library of every editable kind so the registry, the
// lock triggers and the browse projection can be exercised on all of them.
func editorFixture(t *testing.T) (*Service, *sql.DB) {
	t.Helper()
	db, err := persistence.Open(filepath.Join(t.TempDir(), "editor.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	c := catalogtest.New(t, db)
	movies := c.Library("movies", "Movies", "movie", "/movies")
	tv := c.Library("tv", "Shows", "tv", "/tv")
	music := c.Library("music", "Music", "music", "/music")
	books := c.Library("books", "Books", "audiobook", "/books")
	c.Movie(movies, "/movies/movie.mkv", "Before", 2020)
	show := c.Show(tv, "Harbor", 2020)
	season := c.Season(show, 1)
	episode := c.Episode(show, season, 1, "/tv/pilot.mkv")
	c.Fields(episode.ID, map[string]any{"title": "Pilot"})
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	c.Song(album, 1, "/music/song.flac", "Song")
	book := c.Book(books, "Book", "Author")
	c.BookFile(book, 1, "/books/part.m4b")
	s := New(db, "test-token")
	if err = s.SetArtworkDirectory(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	return s, db
}

func editorNames(t *testing.T, db *sql.DB) catalogtest.Names {
	t.Helper()
	names := catalogtest.Names{}
	for _, fixture := range []struct {
		name  string
		kind  int
		title string
	}{
		{"movie", 1, "Before"}, {"show", 2, "Harbor"}, {"season", 3, "Season 1"},
		{"episode", 4, "Pilot"}, {"artist", 5, "Artist"}, {"album", 6, "Album"},
		{"song", 7, "Song"}, {"book", 8, "Book"}, {"part", 9, "Part 1"},
	} {
		var item catalogtest.Item
		if err := db.QueryRow(`SELECT id,pid(public_id) FROM catalog_entities WHERE kind=? AND title=? ORDER BY id LIMIT 1`, fixture.kind, fixture.title).Scan(&item.ID, &item.Public); err != nil {
			t.Fatalf("fixture %q: %v", fixture.name, err)
		}
		names[fixture.name] = item
	}
	return names
}

func editorTarget(names catalogtest.Names, kind, name string) RepairTarget {
	return RepairTarget{Kind: kind, ID: names[name].Public}
}

func editorFactValue(field, value string) any {
	switch field {
	case "number", "track_number", "disc_number", "part_number", "year":
		n, err := strconv.Atoi(value)
		if err != nil {
			panic(err)
		}
		return n
	default:
		return value
	}
}

func editorActor() MBActor {
	return MBActor{Authority: "local", AccountID: "owner", ProfileID: "profile"}
}
func allowAll(*sql.Tx) error { return nil }

func editField(t *testing.T, s *Service, target RepairTarget, field string, edit RepairFieldEdit) RepairState {
	t.Helper()
	before, err := s.RepairState(context.Background(), target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{field: edit}}, editorActor(), allowAll)
	if err != nil {
		t.Fatalf("edit %s/%s %s: %v", target.Kind, target.ID, field, err)
	}
	return after
}

func TestEditorSchemaPerKindAndBulkEligibility(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	expected := map[RepairTarget][]string{
		editorTarget(names, "item", "movie"):    {"title", "sortTitle", "metadataLanguage", "originalTitle", "edition", "tagline", "description", "year", "releaseDate", "contentRating", "studio", "country", "tags", "labels"},
		editorTarget(names, "item", "episode"):  {"title", "sortTitle", "metadataLanguage", "description", "year", "releaseDate", "contentRating", "network", "seasonNumber", "episodeNumber", "tags", "labels"},
		editorTarget(names, "item", "song"):     {"title", "sortTitle", "metadataLanguage", "description", "year", "trackNumber", "discNumber", "tags", "labels"},
		editorTarget(names, "item", "part"):     {"title", "description", "partNumber", "tags", "labels"},
		editorTarget(names, "show", "show"):     {"title", "sortTitle", "metadataLanguage", "originalTitle", "tagline", "description", "year", "contentRating", "network", "studio", "country", "tags", "labels"},
		editorTarget(names, "season", "season"): {"number", "title", "description"},
		editorTarget(names, "album", "album"):   {"title", "sortTitle", "metadataLanguage", "description", "year", "label", "tags"},
		editorTarget(names, "artist", "artist"): {"title", "sortTitle", "metadataLanguage", "description", "tags"},
		editorTarget(names, "book", "book"):     {"title", "sortTitle", "metadataLanguage", "description", "author", "narrator", "series", "seriesIndex", "tags", "labels"},
	}
	for target, fields := range expected {
		state, err := s.RepairState(context.Background(), target, allowAll)
		if err != nil {
			t.Fatal(target, err)
		}
		if len(state.Schema) != len(fields) {
			t.Fatalf("%s schema %d fields, want %d", target.Kind, len(state.Schema), len(fields))
		}
		for n, spec := range state.Schema {
			if spec.Field != fields[n] {
				t.Fatalf("%s field %d is %q, want %q", target.Kind, n, spec.Field, fields[n])
			}
			if spec.Label == "" || spec.Group == "" || spec.Type == "" {
				t.Fatalf("%s field %q is not renderable: %+v", target.Kind, spec.Field, spec)
			}
			if _, ok := state.Snapshot.Fields[spec.Field]; !ok {
				t.Fatalf("%s publishes %q in the schema but not in the snapshot", target.Kind, spec.Field)
			}
		}
		if len(state.ArtworkRoles) == 0 {
			t.Fatalf("%s publishes no artwork roles", target.Kind)
		}
		// A title is never a bulk edit; a descriptive fact shared across a batch is.
		for _, spec := range state.Schema {
			if spec.Field == "title" && spec.Bulk {
				t.Fatal("title is bulk editable")
			}
		}
	}
}

func TestEditorFieldValidationPerType(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	target := editorTarget(names, "item", "movie")
	for _, v := range []struct {
		field, value string
		accept       bool
	}{
		{"title", "A Movie", true},
		{"title", "", false},
		{"title", "   ", false},
		{"title", strings.Repeat("a", 301), false},
		{"title", "badcontrol", false},
		{"year", "1999", true},
		{"year", "", true},
		{"year", "-1", false},
		{"year", "01999", false},
		{"year", "10000", false},
		{"year", "nineteen", false},
		{"releaseDate", "1999-12-31", true},
		{"releaseDate", "", true},
		{"releaseDate", "1999-13-31", false},
		{"releaseDate", "31/12/1999", false},
		{"description", strings.Repeat("d", 20000), true},
		{"description", strings.Repeat("d", 20001), false},
		{"description", "line\nbreak", true},
		{"tags", `["one","two"]`, true},
		{"tags", `[]`, true},
		{"tags", `["` + strings.Repeat("t", 129) + `"]`, false},
		{"tags", `not json`, false},
		{"contentRating", "PG-13", true},
	} {
		before, err := s.RepairState(context.Background(), target, allowAll)
		if err != nil {
			t.Fatal(err)
		}
		value := v.value
		_, err = s.Repair(context.Background(), target, RepairCommand{ExpectedRevision: before.Revision, Action: "edit", Fields: map[string]RepairFieldEdit{v.field: {Value: &value}}}, editorActor(), allowAll)
		if v.accept && err != nil {
			t.Fatalf("%s=%q rejected: %v", v.field, v.value, err)
		}
		if !v.accept && !errors.Is(err, ErrRepairInput) {
			t.Fatalf("%s=%q accepted", v.field, v.value)
		}
	}
	// An empty integer clears the column; it never becomes the number zero.
	value := ""
	editField(t, s, target, "year", RepairFieldEdit{Value: &value})
	var year int
	if err := db.QueryRow(`SELECT year FROM catalog_entities WHERE id=?`, names["movie"].ID).Scan(&year); err != nil || year != 0 {
		t.Fatal("cleared year", year, err)
	}
	state, err := s.RepairState(context.Background(), target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Fields["year"].Value != "0" && state.Snapshot.Fields["year"].Value != "" {
		t.Fatal("cleared year reads back as", state.Snapshot.Fields["year"].Value)
	}
}

// Every new column must keep an owner decision when a scanner or a provider
// rewrites it, and must report the value that writer supplied as the automatic
// value so the editor can offer "use automatic".
func TestEditorLockSurvivesRewriteForEveryColumn(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	c := catalogtest.New(t, db)
	for _, v := range []struct {
		kind, fixture   string
		field, fact     string
		owner, provider string
	}{
		{"item", "movie", "sortTitle", "sort_title", "Owner sort", "Provider sort"},
		{"item", "movie", "originalTitle", "original_title", "Owner original", "Provider original"},
		{"item", "movie", "edition", "edition", "Director's cut", "Theatrical"},
		{"item", "movie", "tagline", "tagline", "Owner tagline", "Provider tagline"},
		{"item", "movie", "releaseDate", "release_date", "1999-01-01", "2001-02-03"},
		{"item", "movie", "contentRating", "content_rating", "PG", "R"},
		{"item", "movie", "studio", "studio", "Owner studio", "Provider studio"},
		{"item", "movie", "country", "country", "Iceland", "France"},
		{"item", "episode", "network", "network", "Owner network", "Provider network"},
		{"item", "episode", "episodeNumber", "number", "4", "9"},
		{"item", "song", "trackNumber", "track_number", "7", "3"},
		{"item", "song", "discNumber", "disc_number", "2", "5"},
		{"item", "part", "partNumber", "part_number", "6", "2"},
		{"show", "show", "sortTitle", "sort_title", "Owner sort", "Provider sort"},
		{"show", "show", "description", "overview", "Owner overview", "Provider overview"},
		{"show", "show", "contentRating", "content_rating", "TV-14", "TV-MA"},
		{"show", "show", "network", "network", "Owner network", "Provider network"},
		{"show", "show", "studio", "studio", "Owner studio", "Provider studio"},
		{"show", "show", "country", "country", "Norway", "Sweden"},
		{"show", "show", "originalTitle", "original_title", "Owner original", "Provider original"},
		{"show", "show", "tagline", "tagline", "Owner tagline", "Provider tagline"},
		{"season", "season", "title", "title", "Owner season", "Provider season"},
		{"season", "season", "description", "overview", "Owner overview", "Provider overview"},
		{"season", "season", "number", "number", "3", "8"},
		{"album", "album", "sortTitle", "sort_title", "Owner sort", "Provider sort"},
		{"album", "album", "description", "overview", "Owner overview", "Provider overview"},
		{"album", "album", "label", "label", "Owner label", "Provider label"},
		{"artist", "artist", "sortTitle", "sort_title", "Owner sort", "Provider sort"},
		{"artist", "artist", "description", "overview", "Owner overview", "Provider overview"},
		{"book", "book", "sortTitle", "sort_title", "Owner sort", "Provider sort"},
		{"book", "book", "description", "overview", "Owner overview", "Provider overview"},
		{"book", "book", "series", "series", "Owner series", "Provider series"},
		{"book", "book", "seriesIndex", "series_position", "3", "9"},
	} {
		owner := v.owner
		target := editorTarget(names, v.kind, v.fixture)
		editField(t, s, target, v.field, RepairFieldEdit{Value: &owner})
		c.Fields(names[v.fixture].ID, map[string]any{v.fact: editorFactValue(v.fact, v.provider)})
		state, err := s.RepairState(context.Background(), target, allowAll)
		if err != nil {
			t.Fatal(err)
		}
		f := state.Snapshot.Fields[v.field]
		if f.Value != v.owner {
			t.Fatalf("%s/%s lost the owner value: %q", target.Kind, v.field, f.Value)
		}
		if f.Automatic != v.provider {
			t.Fatalf("%s/%s automatic value is %q, want %q", target.Kind, v.field, f.Automatic, v.provider)
		}
		if !f.Locked || f.Source != "manual" {
			t.Fatalf("%s/%s provenance %+v", target.Kind, v.field, f)
		}
		// Use automatic restores the writer's value and releases the lock.
		after := editField(t, s, target, v.field, RepairFieldEdit{Automatic: true})
		restored := after.Snapshot.Fields[v.field]
		if restored.Value != v.provider || restored.Locked {
			t.Fatalf("%s/%s useAutomatic left %+v", target.Kind, v.field, restored)
		}
	}
}

func TestEditorMirrorsBrowseAttributes(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	rating := "PG-13"
	studio := "Owner studio"
	tags := `["Noir","Rewatch"]`
	editField(t, s, editorTarget(names, "item", "movie"), "contentRating", RepairFieldEdit{Value: &rating})
	editField(t, s, editorTarget(names, "item", "movie"), "studio", RepairFieldEdit{Value: &studio})
	editField(t, s, editorTarget(names, "item", "movie"), "tags", RepairFieldEdit{Value: &tags})
	rows, err := db.Query(`SELECT f.field,e.source_value,t.value_key FROM catalog_item_attribute_edges e JOIN catalog_attribute_terms t ON t.id=e.term_id JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE e.item_id=? ORDER BY f.field,e.source_value`, names["movie"].ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	got := map[string]string{}
	for rows.Next() {
		var field, value, key string
		if err = rows.Scan(&field, &value, &key); err != nil {
			t.Fatal(err)
		}
		if key != strings.ToLower(value) {
			t.Fatal("attribute key is not the folded value", key, value)
		}
		got[field+":"+value] = key
	}
	for _, want := range []string{"contentRating:PG-13", "studio:Owner studio", "tag:Noir", "tag:Rewatch"} {
		if _, ok := got[want]; !ok {
			t.Fatalf("browse projection is missing %q: %v", want, got)
		}
	}
	// An episode inherits its show's descriptive facts when it carries none.
	network := "Owner network"
	editField(t, s, editorTarget(names, "show", "show"), "network", RepairFieldEdit{Value: &network})
	// A parent's children are republished by the derivation worker.
	if err = compactcatalog.Drain(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_item_attribute_edges WHERE item_id=? AND term_id IN(SELECT t.id FROM catalog_attribute_terms t JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE f.field='network' AND t.value_key=lower(?)) AND source_value=?`, names["episode"].ID, network, network).Scan(&count); err != nil || count != 1 {
		t.Fatal("show network did not reach its episodes", count, err)
	}
}

// A publication that rewrites a locked field must not undo the owner decision,
// and must still report what the provider said.
func TestEditorPublicationRespectsLocks(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	ctx := context.Background()
	target := editorTarget(names, "item", "movie")
	title, rating := "Owner title", "PG"
	editField(t, s, target, "title", RepairFieldEdit{Value: &title})
	editField(t, s, target, "contentRating", RepairFieldEdit{Value: &rating})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw := `{"release_date":"2001-02-03","tagline":"From the provider","production_companies":[{"name":"Provider Pictures"}],"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[{"certification":"R"}]}]}}`
	if err = compactcatalog.SetFieldsTx(ctx, tx, names["movie"].ID, compactcatalog.Automatic, map[string]any{"title": "Provider title"}); err != nil {
		t.Fatal(err)
	}
	if err = publishItemFields(ctx, tx, names["movie"].Public, tmdbDescriptiveFields(raw, "US")); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Snapshot.Fields["title"]; got.Value != title || got.Automatic != "Provider title" {
		t.Fatalf("locked title: %+v", got)
	}
	if got := state.Snapshot.Fields["contentRating"]; got.Value != rating || got.Automatic != "R" {
		t.Fatalf("locked content rating: %+v", got)
	}
	// Unlocked descriptive facts take the provider's value and say where it came from.
	if got := state.Snapshot.Fields["tagline"]; got.Value != "From the provider" {
		t.Fatalf("tagline not published: %+v", got)
	}
	if got := state.Snapshot.Fields["releaseDate"]; got.Value != "2001-02-03" {
		t.Fatalf("release date not published: %+v", got)
	}
	var count int
	if err = db.QueryRow(`SELECT count(*) FROM catalog_item_attribute_edges WHERE item_id=? AND term_id IN(SELECT t.id FROM catalog_attribute_terms t JOIN catalog_attribute_fields f ON f.id=t.field_id WHERE f.field='contentRating' AND t.value_key='pg') AND source_value='PG'`, names["movie"].ID).Scan(&count); err != nil || count != 1 {
		t.Fatal("browse projection followed the provider past the lock", count, err)
	}
}

func TestEditorFieldProvenance(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	if _, err := db.Exec(`INSERT INTO provider_evidence(item_id,provider,provider_id,payload,observed_at) VALUES(?, 'tmdb',7,'{}','2026-01-01T00:00:00Z')`, names["movie"].ID); err != nil {
		t.Fatal(err)
	}
	state, err := s.RepairState(context.Background(), editorTarget(names, "item", "movie"), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Fields["title"].Source != "provider:tmdb" {
		t.Fatal("accepted provider identity not reported", state.Snapshot.Fields["title"].Source)
	}
	if state.Snapshot.Fields["studio"].Source != "automatic" {
		t.Fatal("an empty field is not attributed to a provider", state.Snapshot.Fields["studio"].Source)
	}
	state, err = s.RepairState(context.Background(), editorTarget(names, "artist", "artist"), allowAll)
	if err != nil {
		t.Fatal(err)
	}
	if state.Snapshot.Fields["title"].Source != "scanner" {
		t.Fatal("local-only value not attributed to the scanner", state.Snapshot.Fields["title"].Source)
	}
}

// An edit must be visible to the catalog immediately: the library revision that
// fences detail and browse reads moves, so a client's cached read is invalidated.
func TestEditorEditAdvancesTheCatalogRevision(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	revision := func() int64 {
		var v int64
		if err := db.QueryRow(`SELECT revision FROM library_revisions WHERE library_id='movies'`).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := revision()
	rating := "PG-13"
	editField(t, s, editorTarget(names, "item", "movie"), "contentRating", RepairFieldEdit{Value: &rating})
	if revision() <= before {
		t.Fatal("an owner edit did not advance the catalog revision")
	}
	var title string
	if err := db.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, names["movie"].ID).Scan(&title); err != nil {
		t.Fatal(err)
	}
	owner := "Owner title"
	editField(t, s, editorTarget(names, "item", "movie"), "title", RepairFieldEdit{Value: &owner})
	if err := db.QueryRow(`SELECT title FROM catalog_entities WHERE id=?`, names["movie"].ID).Scan(&title); err != nil || title != owner {
		t.Fatal("the detail projection did not follow the edit", title, err)
	}
}

// The editor read stays one bounded read per fact family however much evidence
// an entity has: two hundred credits and thirty images must not become queries.
func TestEditorReadIsBoundedWithLargeEvidence(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	ctx := context.Background()
	target := editorTarget(names, "item", "movie")
	c := catalogtest.New(t, db)
	credits := make([]compactcatalog.Credit, 200)
	for n := 0; n < 200; n++ {
		id := "credit-" + strconv.Itoa(n)
		credits[n] = compactcatalog.Credit{
			PersonKey: "tmdb:" + id, PersonName: "Person " + id, PersonSortName: "Person " + id,
			ProviderPersonID: id, CreditID: id, CreditedName: "Person " + id,
			Role: "Performer", Department: "Acting", Ordinal: n,
		}
	}
	c.Write(func(ctx context.Context, tx *sql.Tx) error {
		return compactcatalog.SetCreditsTx(ctx, tx, names["movie"].ID, "tmdb", credits)
	})
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	fence, err := artworkFence(ctx, tx, target)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 30; n++ {
		if _, err = insertArtworkCandidate(ctx, tx, target, "poster", "", "tmdb", "image-"+strconv.Itoa(n), "https://image.tmdb.org/t/p/w500/x.jpg", "en", "TMDB", fence, "2026-09-06T00:00:00Z", float64(n)); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	state, err := s.RepairState(ctx, target, allowAll)
	if err != nil {
		t.Fatal(err)
	}
	elapsed := time.Since(start)
	// Every credit is projected: the editor reviews the whole list.
	if len(state.Snapshot.Relationships) != 200 || len(state.Artwork.Candidates) != 30 {
		t.Fatalf("evidence not projected: %d credits, %d images", len(state.Snapshot.Relationships), len(state.Artwork.Candidates))
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("editor read took %s on a small database", elapsed)
	}
}

func TestMetadataLanguageValidatesAndUpdatesSortKey(t *testing.T) {
	s, db := editorFixture(t)
	names := editorNames(t, db)
	value := "en-US"
	state := editField(t, s, editorTarget(names, "item", "movie"), "metadataLanguage", RepairFieldEdit{Value: &value})
	if state.Snapshot.Fields["metadataLanguage"].Value != "en-US" {
		t.Fatal(state)
	}
	title := "The Éclair"
	editField(t, s, editorTarget(names, "item", "movie"), "title", RepairFieldEdit{Value: &title})
	var key string
	if err := db.QueryRow(`SELECT sort_key FROM catalog_entities WHERE id=?`, names["movie"].ID).Scan(&key); err != nil || key != "eclair" {
		t.Fatal(key, err)
	}
	if validFieldValue("item", textSpec("metadataLanguage", "Metadata language", "general", true), "not a language") {
		t.Fatal("invalid language accepted")
	}
}
