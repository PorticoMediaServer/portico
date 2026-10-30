package ingestion

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/decodertest"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/naming"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
	"github.com/PorticoMediaServer/portico/server/internal/testtier"
)

// The Plex music layout's exact examples, built from generated/blank audio
// fixtures. They exercise the real scanner and report the producer's current
// facts; they copy no real media, artwork or provider metadata.

func TestMusicScannerConformancePlexTheWallPackedNumbers(t *testing.T) {
	testtier.Media(t, "generates and scans an actual 26-track music album")
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	albumDir := filepath.Join(root, "music", "Pink Floyd", "The Wall")
	if err := os.MkdirAll(albumDir, 0700); err != nil {
		t.Fatal(err)
	}
	discOne := []string{"In the Flesh?", "The Thin Ice", "Another Brick in the Wall, Part 1", "The Happiest Days of Our Lives", "Another Brick in the Wall, Part 2", "Mother", "Goodbye Blue Sky", "Empty Spaces", "Young Lust", "One of My Turns", "Don't Leave Me Now", "Another Brick in the Wall, Part 3", "Goodbye Cruel World"}
	discTwo := []string{"Hey You", "Is There Anybody Out There?", "Nobody Home", "Vera", "Bring the Boys Back Home", "Comfortably Numb", "The Show Must Go On", "In the Flesh", "Run Like Hell", "Waiting for the Worms", "Stop", "The Trial", "Outside the Wall"}
	var numbers []int
	titles := append(append([]string(nil), discOne...), discTwo...)
	for disc, discTitles := range [][]string{discOne, discTwo} {
		for i, title := range discTitles {
			number := (disc+1)*100 + i + 1
			numbers = append(numbers, number)
			path := filepath.Join(albumDir, fmt.Sprintf("%03d - %s.flac", number, title))
			musicConformanceFLAC(t, ffmpeg, path, "title", title, "album", "The Wall", "artist", "Pink Floyd", "album_artist", "Pink Floyd")
		}
	}

	lib, cat := musicConformanceScan(t, root)
	artist := musicConformanceArtist(t, cat, lib, "Pink Floyd")
	albums, _, err := cat.Albums(catalog.Viewer{Libraries: []string{lib.ID}}, artist, "", 20)
	if err != nil || len(albums) != 1 || albums[0].Title != "The Wall" {
		t.Fatalf("The Wall album: %+v %v", albums, err)
	}
	songs, _, err := cat.AudioItems(catalog.Viewer{Profile: "profile", Libraries: []string{lib.ID}}, albums[0].ID, "album", "", 100)
	if err != nil || len(songs) != len(numbers) {
		t.Fatalf("The Wall tracks: %d %v", len(songs), err)
	}
	cohort, ok := naming.PackedTrackCohort(numbers, false)
	if !ok {
		t.Fatalf("file-name cohort %v is not a clear packed cohort", numbers)
	}
	for i, song := range songs {
		if song.Title != titles[i] || song.Song == nil || song.Song.AlbumTitle != "The Wall" || song.Song.Artist != "Pink Floyd" {
			t.Fatalf("track %d: %+v", i, song)
		}
		raw := numbers[i]
		packed := cohort[raw]
		if song.Song.TrackNumber == nil || *song.Song.TrackNumber != packed.Track || song.Song.DiscNumber == nil || *song.Song.DiscNumber != packed.Disc {
			t.Fatalf("track %d did not commit packed %d/%d: %+v", i, packed.Disc, packed.Track, song.Song)
		}
	}
}

func TestMusicScannerConformancePlexVariousArtistsCompilation(t *testing.T) {
	testtier.Media(t, "generates and scans actual compilation tracks")
	ffmpeg := decodertest.QualifiedFFmpeg(t)
	root := t.TempDir()
	albumDir := filepath.Join(root, "music", "Various Artists", "Guardians Of The Galaxy")
	if err := os.MkdirAll(albumDir, 0700); err != nil {
		t.Fatal(err)
	}
	tracks := []struct {
		number        int
		title, artist string
	}{
		{1, "Hooked on a Feeling", "Blue Swede"},
		{2, "Go All the Way", "The Raspberries"},
	}
	for _, track := range tracks {
		path := filepath.Join(albumDir, fmt.Sprintf("%02d - %s.flac", track.number, track.title))
		musicConformanceFLAC(t, ffmpeg, path, "title", track.title, "track", fmt.Sprint(track.number), "album", "Guardians Of The Galaxy", "artist", track.artist, "album_artist", "Various Artists")
	}

	lib, cat := musicConformanceScan(t, root)
	artists, _, err := cat.Artists(catalog.Viewer{Libraries: []string{lib.ID}}, lib.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	var compilation string
	for _, artist := range artists {
		names[artist.Name] = true
		if artist.Name == "Various Artists" {
			compilation = artist.ID
		}
	}
	if compilation == "" {
		t.Fatalf("compilation album artist missing: %+v", artists)
	}
	for _, track := range tracks {
		if !names[track.artist] {
			t.Fatalf("per-track artist %q missing: %+v", track.artist, artists)
		}
	}
	albums, _, err := cat.Albums(catalog.Viewer{Libraries: []string{lib.ID}}, compilation, "", 20)
	if err != nil || len(albums) != 1 || albums[0].Title != "Guardians Of The Galaxy" {
		t.Fatalf("compilation album: %+v %v", albums, err)
	}
	songs, _, err := cat.AudioItems(catalog.Viewer{Profile: "profile", Libraries: []string{lib.ID}}, albums[0].ID, "album", "", 20)
	if err != nil || len(songs) != len(tracks) {
		t.Fatalf("compilation tracks: %d %v", len(songs), err)
	}
	for i, song := range songs {
		want := tracks[i]
		if song.Title != want.title || song.Song == nil || song.Song.Artist != want.artist || song.Song.AlbumArtist != "Various Artists" || song.Song.AlbumTitle != "Guardians Of The Galaxy" || song.Song.TrackNumber == nil || *song.Song.TrackNumber != want.number {
			t.Fatalf("compilation track %d: %+v", i, song)
		}
	}
}

// Decoder acceptance cannot stand in for Basic ingestion: the exact bounded
// header timeline must survive the real probe, scanner and normalized writer.
func TestMusicScannerConformanceMusepackBasicStoresValidatedTimeline(t *testing.T) {
	testtier.Media(t, "probes and scans actual committed SV7 and short SV8 tones")
	root := t.TempDir()
	albumDir := filepath.Join(root, "music", "Fixture Artist", "Musepack")
	if err := os.MkdirAll(albumDir, 0700); err != nil {
		t.Fatal(err)
	}
	names := []string{"tone-sv7.mpc", "tone-sv8.mpc"}
	for _, name := range names {
		raw, err := os.ReadFile(filepath.Join("..", "decoder", "testdata", "music-formats-r29", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(albumDir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, db := musicConformanceScanWithDB(t, root)
	for index, name := range names {
		var codec, measuredCodec string
		var duration float64
		var sampleRate, channels int
		var sourceCurrent bool
		err := db.QueryRow(`SELECT asset.audio_codec,asset.duration,facts.codec,facts.sample_rate,facts.channels,facts.size=asset.size AND facts.modified_ns=asset.modified_ns FROM catalog_assets asset JOIN music_technical_facts facts ON facts.asset_id=asset.token WHERE asset.path=? AND asset.available=1`, filepath.Join(albumDir, name)).Scan(&codec, &duration, &measuredCodec, &sampleRate, &channels, &sourceCurrent)
		expected := []string{"mpc7", "mpc8"}[index]
		if err != nil || codec != expected || measuredCodec != expected || duration != 0.5 || sampleRate != 44100 || channels != 2 || !sourceCurrent {
			t.Fatal("Basic scan did not publish the actual validated source timeline", name, codec, duration, measuredCodec, sampleRate, channels, sourceCurrent, err)
		}
	}
}

func musicConformanceFLAC(t *testing.T, ffmpeg, path string, tags ...string) {
	t.Helper()
	args := []string{"-nostdin", "-y", "-v", "error", "-f", "lavfi", "-i", "sine=frequency=440:duration=0.2", "-c:a", "flac"}
	for i := 0; i+1 < len(tags); i += 2 {
		args = append(args, "-metadata", tags[i]+"="+tags[i+1])
	}
	args = append(args, path)
	if out, err := exec.Command(ffmpeg, args...).CombinedOutput(); err != nil {
		t.Fatalf("generated fixture %s: %v %s", filepath.Base(path), err, out)
	}
}

func musicConformanceScan(t *testing.T, root string) (catalog.Library, *catalog.Service) {
	t.Helper()
	library, service, _ := musicConformanceScanWithDB(t, root)
	return library, service
}

func musicConformanceScanWithDB(t *testing.T, root string) (catalog.Library, *catalog.Service, *sql.DB) {
	t.Helper()
	ffprobe := decodertest.QualifiedFFprobe(t)
	db, err := persistence.Open(filepath.Join(root, "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	cat := catalog.New(db)
	cat.SetStorage(store)
	meta, err := localmetadata.New(filepath.Join(root, "artwork"), decodertest.QualifiedFFmpeg(t), store)
	if err != nil {
		t.Fatal(err)
	}
	scanner := New(db, cat, assets.Probe{Binary: ffprobe, Supervisor: store.Supervisor})
	scanner.SetStorage(store)
	scanner.LocalMetadata = meta
	lib, err := cat.Create("Music", "music", filepath.Join(root, "music"))
	if err != nil {
		t.Fatal(err)
	}
	job, err := scanner.Queue(lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 200; n++ {
		scanner.process(context.Background(), job.ID, lib.ID)
		state, err := scanner.Get(job.ID)
		if err != nil {
			t.Fatal(err)
		}
		switch state.Status {
		case "complete", "complete_with_warnings":
			t.Logf("music scan finished %s, processed=%d warnings=%d", state.Status, state.Processed, state.Warnings)
			// The focused scanner below has no production deep-analysis worker,
			// so its only permissible warning is that worker being unavailable.
			// Any other code would mean the fixture scan itself failed to publish.
			rows, err := db.Query(`SELECT DISTINCT analysis_error FROM inventory_objects WHERE analysis_error<>''`)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var code string
				if err = rows.Scan(&code); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				if code != "analysis_worker_unavailable" {
					rows.Close()
					t.Fatalf("unexpected fixture scan warning: %s", code)
				}
			}
			if err = rows.Close(); err != nil {
				t.Fatal(err)
			}
			catalogtest.New(t, db).Drain()
			return lib, cat, db
		case "failed", "cancelled":
			t.Fatalf("music scan %s: %+v", state.Status, state)
		}
	}
	t.Fatal("music scan did not finish")
	return catalog.Library{}, nil, nil
}

func musicConformanceArtist(t *testing.T, cat *catalog.Service, lib catalog.Library, name string) string {
	t.Helper()
	artists, _, err := cat.Artists(catalog.Viewer{Libraries: []string{lib.ID}}, lib.ID, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	for _, artist := range artists {
		if artist.Name == name {
			return artist.ID
		}
	}
	t.Fatalf("artist %q missing: %+v", name, artists)
	return ""
}
