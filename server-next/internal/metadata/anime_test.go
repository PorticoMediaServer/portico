package metadata_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/anilist/anilisttest"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/tmdb/tmdbtest"
)

// An anime library on AniList, from AniList's recorded answers: a show whose AniList title
// carries its year ("Hunter x Hunter (2011)") and one whose name has a macron ("Naruto
// Shippūden", which AniList's search finds only without it) are both matched. AniList names no
// episodes, so an episode is named by the name in its file, else "Episode 1".
func TestAnAniListLibraryMatchesRemakesAndAccentedNamesAndNamesEpisodesFromFiles(t *testing.T) {
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	root := filepath.Join(t.TempDir(), "library")
	for _, rel := range []string{"Hunter x Hunter (2011)/Season 01/Hunter x Hunter S01E01.mp4",
		"Naruto Shippūden (2007)/Season 01/Naruto Shippūden - S01E01 - Homecoming [WEBDL-1080p].mkv"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(rel), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	f := scanned(t, d, db.LibraryAnime, root, tmdbtest.New(t))
	provider := "anilist"
	if _, _, err := d.EditLibrary(ctx, f.lib.PublicID, db.LibraryEdit{MetadataProvider: &provider}); err != nil {
		t.Fatal(err)
	}
	f.s.Providers = map[string]metadata.Provider{"anilist": anilist.New(anilisttest.New(t).URL, 0)}
	if stats := f.match(); stats.Matched != 2 || stats.Unmatched != 0 {
		t.Fatalf("matched %d, unmatched %d; want both shows matched", stats.Matched, stats.Unmatched)
	}
	if show := f.title(db.KindShow, "Hunter x Hunter"); show.Year != 2011 {
		t.Errorf("Hunter x Hunter: year %d; want the 2011 series", show.Year)
	}
	if show := f.title(db.KindShow, "Naruto: Shippuden"); show.Year != 2007 {
		t.Errorf("Naruto Shippuden: year %d; want the 2007 series", show.Year)
	}
	f.title(db.KindEpisode, "Homecoming")
	f.title(db.KindEpisode, "Episode 1")
}

// The provider's episode name beats the one in the file name (definition area 4, rule 6), and
// a later scan does not put the file's back.
func TestTheProvidersEpisodeNameBeatsTheFileNames(t *testing.T) {
	f := newFixture(t, db.LibraryShows, "Breaking Bad/Season 01/Breaking Bad - S01E01 - The First One WEBDL-1080p.mkv")
	f.title(db.KindEpisode, "The First One")
	f.match()
	f.title(db.KindEpisode, "Pilot")
	f.rescan()
	f.title(db.KindEpisode, "Pilot")
}
