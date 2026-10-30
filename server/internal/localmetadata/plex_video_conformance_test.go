package localmetadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Plex and Kodi both read movie.nfo beside a film, tvshow.nfo above a season
// folder, and a per-episode .nfo named like the episode. The generated XML here
// carries no real provider response; it only proves the sidecar layout is read
// and its typed evidence reaches the scan facts.
func TestPlex45VideoNFOMovieShowAndEpisodeLayouts(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(relative, body string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	movieNFO := `<movie><title>Batman Begins</title><year>2005</year><edition>Deluxe</edition><uniqueid type="imdb">tt0372784</uniqueid></movie>`
	showNFO := `<tvshow><title>Band of Brothers</title><year>2001</year><uniqueid type="tvdb">4613</uniqueid></tvshow>`
	episodeNFO := `<episodedetails><title>Currahee</title><season>1</season><episode>1</episode></episodedetails>`
	write("Movies/Batman Begins (2005)/Batman Begins (2005).mkv", "media")
	write("Movies/Batman Begins (2005)/movie.nfo", movieNFO)
	write("Shows/Band of Brothers (2001)/tvshow.nfo", showNFO)
	write("Shows/Band of Brothers (2001)/Season 01/Band of Brothers - s01e01 - Currahee.mkv", "media")
	write("Shows/Band of Brothers (2001)/Season 01/Band of Brothers - s01e01 - Currahee.nfo", episodeNFO)

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(t.TempDir(), "", storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}

	movie := service.VideoNFO(context.Background(), "library", root, filepath.Join(root, "Movies", "Batman Begins (2005)", "Batman Begins (2005).mkv"), "movie", assets.Facts{})
	if movie.VideoMetadataStatus != "observed" || movie.LocalMetadataIssue != "" {
		t.Fatalf("movie NFO status=%q issue=%q", movie.VideoMetadataStatus, movie.LocalMetadataIssue)
	}
	if len(movie.VideoMetadata) != 1 {
		t.Fatalf("movie NFO records: %+v", movie.VideoMetadata)
	}
	record := movie.VideoMetadata[0]
	if record.Kind != "movie" || record.Title != "Batman Begins" || record.Year != 2005 || record.Edition != "Deluxe" {
		t.Fatalf("movie NFO evidence: %+v", record)
	}
	found := false
	for _, id := range record.IDs {
		if id.Provider == "imdb" && id.Type == "title" && id.ID == "tt0372784" {
			found = true
		}
	}
	if !found {
		t.Fatalf("movie NFO lost its ID: %+v", record.IDs)
	}
	if record.Locator == "" || record.Digest == "" || record.Size != int64(len(movieNFO)) {
		t.Fatalf("movie NFO custody evidence: %+v", record)
	}

	episode := service.VideoNFO(context.Background(), "library", root, filepath.Join(root, "Shows", "Band of Brothers (2001)", "Season 01", "Band of Brothers - s01e01 - Currahee.mkv"), "tv", assets.Facts{})
	if episode.VideoMetadataStatus != "observed" || episode.LocalMetadataIssue != "" {
		t.Fatalf("episode NFO status=%q issue=%q", episode.VideoMetadataStatus, episode.LocalMetadataIssue)
	}
	if len(episode.VideoMetadata) != 2 {
		t.Fatalf("episode and show NFO records: %+v", episode.VideoMetadata)
	}
	var gotShow, gotEpisode bool
	for _, record := range episode.VideoMetadata {
		switch record.Kind {
		case "show":
			gotShow = record.Title == "Band of Brothers" && record.Year == 2001 && record.Size == int64(len(showNFO))
		case "episode":
			gotEpisode = record.Title == "Currahee" && record.Season != nil && *record.Season == 1 && record.Episode != nil && *record.Episode == 1 && record.Size == int64(len(episodeNFO))
		}
	}
	if !gotShow || !gotEpisode {
		t.Fatalf("show NFO from the show folder or episode NFO beside the file was lost: %+v", episode.VideoMetadata)
	}
}
