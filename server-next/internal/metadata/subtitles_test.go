package metadata_test

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/fetch"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles"
	"github.com/PorticoMediaServer/portico/server-next/internal/metadata/opensubtitles/opensubtitlestest"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/seed"
)

// film makes a local-metadata-only library holding The Matrix, with its IMDb id.
func film(t *testing.T) (*db.DB, db.Viewer, string) {
	t.Helper()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	if _, err := d.NewLibrary(ctx, db.NewLibrary{Name: "Films", Kind: db.LibraryMovies, Language: "en", MetadataProvider: db.ProviderLocal, Folders: []string{t.TempDir()}}); err != nil {
		t.Fatal(err)
	}
	viewers, err := seed.Profiles(ctx, d, 1)
	if err != nil {
		t.Fatal(err)
	}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ref := db.FileRef{Name: "The Matrix (1999).mkv"}
	_, err = d.Import(ctx, db.ImportBatch{LibraryID: libraries[0].ID, PathID: libraries[0].Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}},
		Files: []db.ImportFile{{FileRef: ref, Role: db.FileMedia, Size: 1000, Container: "mkv", DurationMS: 60_000,
			Streams: []db.Stream{{Kind: db.StreamVideo, Codec: "h264"}}}},
		Items: []db.ImportItem{{Kind: db.KindMovie, Title: "The Matrix", Year: 1999, Keys: []db.ItemKey{{Kind: db.KeyFile, Key: ref.Name}},
			ExternalIDs: []db.ExternalID{{Provider: "imdb", ID: "tt0133093"}}, Versions: []db.ImportVersion{{Parts: []db.ImportPart{{File: ref}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := d.Items(ctx, viewers[0], db.ItemQuery{Library: libraries[0].PublicID, Kinds: []db.ItemKind{db.KindMovie}, Limit: 1})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	return d, viewers[0], page.Items[0].PublicID
}

// downloaded are the downloaded subtitles playback offers for the film's version.
func downloaded(t *testing.T, d *db.DB, v db.Viewer, id string) []db.Track {
	t.Helper()
	item, err := d.PlaybackItem(ctx, v, id)
	if err != nil {
		t.Fatal(err)
	}
	var out []db.Track
	for _, track := range item.Versions[0].Tracks {
		if track.Downloaded != "" {
			out = append(out, track)
		}
	}
	return out
}

func TestASubtitleIsFoundDownloadedAndOfferedAtPlayback(t *testing.T) {
	d, viewer, id := film(t)
	server := opensubtitlestest.New(t)
	s := &metadata.Service{DB: d, SubtitleDir: t.TempDir()}
	libraries, err := d.AllLibraries(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if asks, err := s.Asks(ctx, libraries[0].PublicID); err != nil || asks {
		t.Fatalf("local-only library starts automatic lookups: %v, %v", asks, err)
	}

	// Without the owner's OpenSubtitles account there is nothing to search.
	if _, err := s.SearchSubtitles(ctx, viewer, id, []string{"eng"}); !errors.Is(err, metadata.ErrNoSubtitleProvider) {
		t.Fatalf("search without a provider: %v", err)
	}
	// A refused password says so, and is not tried again.
	s.UseSubtitles(opensubtitles.New(server.URL, 0, "key", "owner", "wrong"))
	for range 2 {
		if _, err := s.SearchSubtitles(ctx, viewer, id, []string{"eng"}); !errors.Is(err, opensubtitles.ErrSignInRefused) {
			t.Fatalf("a wrong password: %v", err)
		}
	}
	if n := server.Requests.Load(); n != 1 {
		t.Errorf("a refused sign-in was tried %d times", n)
	}

	s.UseSubtitles(opensubtitles.New(server.URL, 0, "key", "owner", "right"))
	results, err := s.SearchSubtitles(ctx, viewer, id, []string{"eng"})
	if err != nil || len(results) != 2 {
		t.Fatalf("search: %+v, %v", results, err)
	}
	if r := results[1]; r.Language != "eng" || !r.HearingImpaired || r.Release != "The.Matrix.1999.SDH" {
		t.Errorf("second result: %+v", r)
	}

	// The download is kept with the film and offered to everyone who may play it.
	sub, err := s.DownloadSubtitle(ctx, viewer, id, "", results[0])
	if err != nil {
		t.Fatal(err)
	}
	tracks := downloaded(t, d, viewer, id)
	if len(tracks) != 1 || tracks[0].Language != "eng" || tracks[0].Codec != "srt" || playback.TrackID(tracks[0]) != "d"+sub.PublicID {
		t.Fatalf("offered at playback: %+v", tracks)
	}
	path := metadata.SubtitlePath(s.SubtitleDir, sub.FileHash)
	if raw, err := os.ReadFile(path); err != nil || len(raw) == 0 {
		t.Fatalf("the subtitle's file: %v", err)
	}
	// The same subtitle again is the one already kept, and OpenSubtitles is not asked: a
	// download counts against the account's daily allowance.
	asked := server.Requests.Load()
	if again, err := s.DownloadSubtitle(ctx, viewer, id, "", results[0]); err != nil || again.PublicID != sub.PublicID {
		t.Errorf("a second download: %+v, %v", again, err)
	}
	if n := server.Requests.Load() - asked; n != 0 {
		t.Errorf("downloading the same subtitle again sent OpenSubtitles %d requests", n)
	}
	if n := len(downloaded(t, d, viewer, id)); n != 1 {
		t.Errorf("%d subtitles after downloading the same one twice", n)
	}

	// A used-up allowance says so, and stores nothing.
	server.AllowanceUsed.Store(true)
	var used *opensubtitles.AllowanceUsed
	if _, err := s.DownloadSubtitle(ctx, viewer, id, "", results[1]); !errors.As(err, &used) {
		t.Errorf("a used-up allowance: %v", err)
	}

	// Removed, it is gone from playback and from the data folder.
	if err := s.RemoveSubtitle(ctx, sub.PublicID); err != nil {
		t.Fatal(err)
	}
	if n := len(downloaded(t, d, viewer, id)); n != 0 {
		t.Errorf("%d subtitles after removing it", n)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("the removed subtitle's file: %v", err)
	}
}

// The subtitle file is fetched through the same guard as every provider request: from a test,
// an address OpenSubtitles gives on its real file host is refused as a live call, not fetched.
func TestTheSubtitleFileGoesThroughTheProviderGuard(t *testing.T) {
	d, viewer, id := film(t)
	server := opensubtitlestest.New(t)
	s := &metadata.Service{DB: d, SubtitleDir: t.TempDir()}
	s.UseSubtitles(opensubtitles.New(server.URL, 0, "key", "owner", "right"))
	results, err := s.SearchSubtitles(ctx, viewer, id, []string{"eng"})
	if err != nil || len(results) == 0 {
		t.Fatalf("search: %+v, %v", results, err)
	}
	server.LinkElsewhere.Store(true)
	if _, err := s.DownloadSubtitle(ctx, viewer, id, "", results[0]); !errors.Is(err, fetch.ErrLiveCallInTest) {
		t.Fatalf("a file on OpenSubtitles' real host, from a test: %v; want it refused as a live call", err)
	}
	if n := len(downloaded(t, d, viewer, id)); n != 0 {
		t.Fatalf("%d subtitles kept after the file was refused", n)
	}
}

// Two downloads of the same subtitle for the same version at once (a double press, a retried
// request) spend one of the account's daily downloads: the second answers the first's.
func TestTwoDownloadsOfOneSubtitleAtOnceSpendOne(t *testing.T) {
	d, viewer, id := film(t)
	server := opensubtitlestest.New(t)
	s := &metadata.Service{DB: d, SubtitleDir: t.TempDir()}
	s.UseSubtitles(opensubtitles.New(server.URL, 0, "key", "owner", "right"))
	results, err := s.SearchSubtitles(ctx, viewer, id, []string{"eng"})
	if err != nil || len(results) == 0 {
		t.Fatalf("search: %+v, %v", results, err)
	}
	server.SlowDownloads.Store(true)
	var wg sync.WaitGroup
	got := make([]db.Subtitle, 2)
	errs := make([]error, 2)
	for i := range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			got[i], errs[i] = s.DownloadSubtitle(ctx, viewer, id, "", results[0])
		}()
	}
	wg.Wait()
	if errs[0] != nil || errs[1] != nil || got[0].PublicID != got[1].PublicID {
		t.Fatalf("two downloads at once: %+v %v, %+v %v", got[0], errs[0], got[1], errs[1])
	}
	if n := server.Downloads.Load(); n != 1 {
		t.Fatalf("two downloads of one subtitle at once asked OpenSubtitles for %d downloads; want 1", n)
	}
}
