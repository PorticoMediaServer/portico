package metadata_test

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// A provider never replaces what a title's own files say. A film's NFO file names it, rates
// it and describes it; the provider it is matched to gives another title, rating and
// description. The file's values stand, and the provider only fills what the file left out.
// When the NFO file changes, its new values stand too, against the same provider.
func TestAProviderNeverReplacesWhatAnNFOFileGave(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	root := filepath.Join(t.TempDir(), "library")
	folder := filepath.Join(root, "Home Copy")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(folder, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("film.mkv", "not really a film")
	write("movie.nfo", `<movie><title>Our Matrix</title><plot>As the file tells it.</plot><mpaa>PG</mpaa><uniqueid type="tmdb">603</uniqueid></movie>`)
	f := scanned(t, d, db.LibraryMovies, root, tmdbtest.New(t))

	film := func(title string) db.ItemDetail {
		t.Helper()
		page, err := f.d.Items(ctx, f.viewer, db.ItemQuery{Library: f.lib.PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 10})
		if err != nil || len(page.Items) != 1 {
			t.Fatalf("films: %d, %v", len(page.Items), err)
		}
		detail, err := f.d.Item(ctx, f.viewer, page.Items[0].PublicID)
		if err != nil {
			t.Fatal(err)
		}
		if detail.Title != title {
			t.Fatalf("the film is called %q; the NFO file says %q", detail.Title, title)
		}
		return detail
	}
	before := film("Our Matrix")

	// TMDB's film 603 is "The Matrix", rated R, with its own description.
	if stats := f.match(); stats.Matched != 1 {
		t.Fatalf("matched %d; want the film, by the id in its NFO file", stats.Matched)
	}
	matched := film("Our Matrix")
	if matched.PublicID != before.PublicID || matched.ContentRating != "PG" || matched.Summary != "As the file tells it." {
		t.Errorf("the provider replaced what the NFO file gave: rating %q, description %q", matched.ContentRating, matched.Summary)
	}
	// What the file did not say is the provider's.
	if matched.Tagline != "Believe the unbelievable." || !slices.Equal(tagNames(matched, db.TagGenre), []string{"Action", "Science Fiction"}) {
		t.Errorf("the provider did not fill what the file left out: tagline %q, genres %q", matched.Tagline, tagNames(matched, db.TagGenre))
	}

	// The NFO file changes: the same title, with the file's new values.
	write("movie.nfo", `<movie><title>Our Matrix, Recut</title><plot>Told again.</plot><mpaa>PG-13</mpaa><uniqueid type="tmdb">603</uniqueid></movie>`)
	if _, err := (&scan.Scanner{DB: f.d, Prober: prober{}}).Scan(ctx, f.lib); err != nil {
		t.Fatal(err)
	}
	reread := film("Our Matrix, Recut")
	if reread.PublicID != before.PublicID || reread.ContentRating != "PG-13" || reread.Summary != "Told again." {
		t.Errorf("after the NFO file changed: rating %q, description %q", reread.ContentRating, reread.Summary)
	}
	if reread.Tagline != "Believe the unbelievable." {
		t.Errorf("reading the file again lost the provider's tagline: %q", reread.Tagline)
	}

	// And they stand against the provider's next answer.
	adult := 17
	again := db.ItemDetails{Title: "The Matrix", Summary: "The provider's words.", ContentRating: "R", RatingAge: &adult, Year: 1999}
	if err := f.d.ApplyDetails(ctx, db.Background, reread.ID, again); err != nil {
		t.Fatal(err)
	}
	if after := film("Our Matrix, Recut"); after.ContentRating != "PG-13" || after.Summary != "Told again." || after.Year != 1999 {
		t.Errorf("a later answer replaced the file's values, or did not fill the year: %+v", after)
	}
}
