package metadata_test

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

func text(s string) *string { return &s }

func TestARefreshNeverOverwritesAHandEdit(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv")
	f.match()
	film := f.title(db.KindMovie, "The Matrix")
	locks, err := f.d.MetadataLocks(ctx, film.PublicID)
	if err != nil || len(locks.Locked) != 0 {
		t.Fatalf("locks before any edit: %+v, %v", locks, err)
	}

	// A person edits the title, the description, the tagline and the genres: each is locked.
	year := 12345
	if _, err := f.d.EditMetadata(ctx, film.PublicID, locks.Revision, db.MetadataEdit{Year: &year}); !errors.Is(err, db.ErrInvalid) {
		t.Fatalf("a year out of range: %v", err)
	}
	genres := []string{"Cyberpunk", "cyberpunk", " "}
	revision, err := f.d.EditMetadata(ctx, film.PublicID, locks.Revision, db.MetadataEdit{Title: text("The Matrix (my copy)"),
		Summary: text("Mine."), Tagline: text("My tagline."), ContentRating: text("PG-13"), Genres: &genres})
	if err != nil {
		t.Fatal(err)
	}
	// Someone else's editor, opened before that save, overwrites nothing.
	if _, err := f.d.EditMetadata(ctx, film.PublicID, locks.Revision, db.MetadataEdit{Summary: text("Theirs.")}); !errors.Is(err, db.ErrStale) {
		t.Fatalf("an edit on an out-of-date copy: %v", err)
	}
	locks, _ = f.d.MetadataLocks(ctx, film.PublicID)
	if want := []string{"contentRating", "genres", "summary", "tagline", "title"}; !slices.Equal(locks.Locked, want) || locks.Revision != revision {
		t.Fatalf("locks after the edit: %+v", locks)
	}

	// The editor can tell what a person typed from what is only locked: the year is locked
	// here without being changed, and stays the provider's.
	if _, err := f.d.EditMetadata(ctx, film.PublicID, revision, db.MetadataEdit{Lock: []string{"year"}}); err != nil {
		t.Fatal(err)
	}
	locks, _ = f.d.MetadataLocks(ctx, film.PublicID)
	if locks.Sources["title"] != db.SourceEdit || locks.Sources["year"] != "" || !slices.Contains(locks.Locked, "year") {
		t.Fatalf("sources after an edit and a lock: %+v", locks)
	}

	asked := f.tmdb.Requests.Load()
	if refreshed, err := f.s.Refresh(ctx, film.PublicID); err != nil || !refreshed || f.tmdb.Requests.Load() == asked {
		t.Fatalf("Refresh: %v, %v", refreshed, err)
	}
	edited := f.title(db.KindMovie, "The Matrix (my copy)")
	if edited.Summary != "Mine." || edited.Tagline != "My tagline." || edited.ContentRating != "PG-13" ||
		!slices.Equal(tagNames(edited, db.TagGenre), []string{"Cyberpunk"}) {
		t.Errorf("a refresh overwrote a hand edit: %+v", edited)
	}
	// What nobody edited is still the provider's.
	if edited.Year != 1999 || !slices.Equal(tagNames(edited, db.TagStudio), []string{"Village Roadshow Pictures", "Warner Bros. Pictures"}) {
		t.Errorf("a refresh lost the provider's details: %+v", edited)
	}

	// Unlocking lets the provider's value return at the next refresh; the rest stay locked.
	locks, _ = f.d.MetadataLocks(ctx, film.PublicID)
	if _, err := f.d.EditMetadata(ctx, film.PublicID, locks.Revision, db.MetadataEdit{Unlock: []string{"tagline", "genres"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Refresh(ctx, film.PublicID); err != nil {
		t.Fatal(err)
	}
	unlocked := f.title(db.KindMovie, "The Matrix (my copy)")
	if unlocked.Tagline != "Believe the unbelievable." || !slices.Equal(tagNames(unlocked, db.TagGenre), []string{"Action", "Science Fiction"}) {
		t.Errorf("an unlocked field did not take the provider's value: %+v", unlocked)
	}
	if unlocked.Summary != "Mine." {
		t.Errorf("a field still locked changed: %q", unlocked.Summary)
	}
	// The unlocked tagline is the provider's again; the description is still the person's.
	locks, _ = f.d.MetadataLocks(ctx, film.PublicID)
	if locks.Sources["tagline"] != "" || locks.Sources["summary"] != db.SourceEdit {
		t.Errorf("sources after unlocking and refreshing: %+v", locks.Sources)
	}

	// A provider that cannot be asked changes nothing.
	f.tmdb.Down.Store(true)
	if _, err := f.s.Refresh(ctx, film.PublicID); err == nil {
		t.Error("a provider that is down was not reported")
	}
	if after := f.title(db.KindMovie, "The Matrix (my copy)"); after.Tagline != unlocked.Tagline || after.CreditTotal != unlocked.CreditTotal {
		t.Errorf("a failed refresh changed the title: %+v", after)
	}
}

func TestTitlesAreRefreshedWhenTheyAreDue(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv", "Zzyzx Home Video (2011).mkv")
	f.match()
	now := time.Now()
	refresh := func(at time.Time) (int, int64) {
		t.Helper()
		asked := f.tmdb.Requests.Load()
		done, err := f.s.RefreshLibrary(ctx, f.lib.ID, at, 100)
		if err != nil {
			t.Fatal(err)
		}
		return done, f.tmdb.Requests.Load() - asked
	}
	// Just matched: nothing is due, and the provider is not asked.
	if done, asked := refresh(now.Add(time.Hour)); done != 0 || asked != 0 {
		t.Errorf("an hour after matching: %d refreshed, %d requests", done, asked)
	}
	// A title added in the last two weeks is refreshed after two days. The unmatched one never is.
	if done, asked := refresh(now.Add(3 * 24 * time.Hour)); done != 1 || asked != 1 {
		t.Errorf("three days after matching: %d refreshed, %d requests", done, asked)
	}
	// It was refreshed just now, by the real clock: once it is no longer new it waits 30 days.
	if done, _ := refresh(now.Add(20 * 24 * time.Hour)); done != 0 {
		t.Errorf("twenty days on, a film was refreshed %d times; it is due at thirty", done)
	}
	if done, _ := refresh(now.Add(31 * 24 * time.Hour)); done != 1 {
		t.Errorf("thirty-one days on: %d refreshed; want 1", done)
	}
	if film := f.title(db.KindMovie, "The Matrix"); !strings.HasPrefix(film.Summary, "Set in the 22nd century") {
		t.Errorf("after refreshes: %+v", film)
	}
}

func TestANewEpisodeOfAMatchedShowIsLookedUpAtOnce(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01.mkv")
	f.match()
	// The show ended long ago and was matched a moment ago: by its age it is not due.
	file := filepath.Join(f.lib.Paths[0].Path, "Breaking Bad", "Season 01", "Breaking Bad - S01E02.mkv")
	if err := os.WriteFile(file, make([]byte, 222), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := (&scan.Scanner{DB: f.d, Prober: prober{}}).Scan(ctx, f.lib); err != nil {
		t.Fatal(err)
	}
	f.title(db.KindEpisode, "Episode 2")
	if done, err := f.s.RefreshLibrary(ctx, f.lib.ID, time.Now(), 100); err != nil || done != 1 {
		t.Fatalf("after a scan added an episode: %d refreshed, %v; want the show", done, err)
	}
	f.title(db.KindEpisode, "Cat's in the Bag...")
	// Nothing more was added: the show is not refreshed again.
	if done, err := f.s.RefreshLibrary(ctx, f.lib.ID, time.Now(), 100); err != nil || done != 0 {
		t.Errorf("a second run: %d refreshed, %v", done, err)
	}
}

func TestAllTitlesAreLookedThroughAtMostHourly(t *testing.T) {
	f := newFixture(t, db.LibraryMovies, "the.matrix.1999.1080p.mkv")
	f.match()
	due := time.Now().Add(2 * 24 * time.Hour) // when the film, being new, is first due
	for _, step := range []struct {
		at   time.Duration
		want int
		why  string
	}{
		{-40 * time.Minute, 0, "not due yet"},
		{10 * time.Minute, 0, "due, but every title was looked through 50 minutes ago"},
		{30 * time.Minute, 1, "due, and the last look through was over an hour ago"},
	} {
		if done, err := f.s.RefreshLibrary(ctx, f.lib.ID, due.Add(step.at), 100); err != nil || done != step.want {
			t.Errorf("%s: %d refreshed, %v; want %d", step.why, done, err, step.want)
		}
	}
}
