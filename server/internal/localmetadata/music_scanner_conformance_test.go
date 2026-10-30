package localmetadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// The Plex music layout's exact examples, built from generated/blank fixture
// files. This is a read-only conformance audit: it uses the package's one
// artwork resolver and the existing storage lyric reader, and changes neither.

func TestMusicScannerConformancePlexArtworkRoles(t *testing.T) {
	root := t.TempDir()
	musicRoot := filepath.Join(root, "music")
	artistDir := filepath.Join(musicRoot, "Pink Floyd")
	albumDir := filepath.Join(artistDir, "The Wall")
	if err := os.MkdirAll(albumDir, 0700); err != nil {
		t.Fatal(err)
	}
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(artistDir, "artist-poster.jpg"), musicImageFixture(t, "jpg", 20))
	write(filepath.Join(artistDir, "artist-background.jpg"), musicImageFixture(t, "jpg", 40))
	write(filepath.Join(albumDir, "cover.png"), musicImageFixture(t, "png", 60))
	write(filepath.Join(albumDir, "background.webp"), musicImageFixture(t, "webp", 80))
	media := filepath.Join(albumDir, "101 - In the Flesh.flac")
	write(media, nil)

	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(filepath.Join(root, "artwork"), "", storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}
	got := service.ReadSidecars(context.Background(), "music", "music", musicRoot, media, -1)
	for role, want := range map[string]string{
		"artist/portrait": "image/jpeg",
		"artist/backdrop": "image/jpeg",
		"album/cover":     "image/png",
		"album/backdrop":  "image/webp",
	} {
		key := got[role]
		if key == "" {
			t.Fatalf("Plex artwork role missing: %s %+v", role, got)
		}
		file, mime, err := service.Open(key)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		if mime != want {
			t.Fatalf("%s mime %q, want %q", role, mime, want)
		}
	}

	facts := service.PrepareKind(context.Background(), "music", "music", musicRoot, media, assets.Facts{})
	if facts.ArtworkKey == "" {
		t.Fatal("album cover did not become the track's local artwork")
	}
}

func TestMusicScannerConformancePlexLyricsSidecars(t *testing.T) {
	root := t.TempDir()
	musicRoot := filepath.Join(root, "music")
	albumDir := filepath.Join(musicRoot, "Bob Schneider", "Underneath the Onion Trees")
	if err := os.MkdirAll(albumDir, 0700); err != nil {
		t.Fatal(err)
	}
	media := filepath.Join(albumDir, "01 - Blue Skies.flac")
	write := func(path string, data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(media, nil)
	write(filepath.Join(albumDir, "01 - Blue Skies.lrc"), []byte("[00:01.00]Blue skies"))
	write(filepath.Join(albumDir, "01 - Blue Skies.txt"), []byte("Blue skies\n"))
	// Another track's lyrics never attach by folder proximity.
	write(filepath.Join(albumDir, "02 - Other.lrc"), []byte("[00:01.00]Other"))

	info, err := os.Stat(media)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := storage.New(binary).ReadLyrics(context.Background(), musicRoot, media, info.Size(), info.ModTime().UnixNano(), "und", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Candidates) != 2 {
		t.Fatalf("lyric candidates %+v warnings %v", got.Candidates, got.Warnings)
	}
	for i, want := range []struct {
		format, label, data string
	}{
		{"lrc", "01 - Blue Skies.lrc", "[00:01.00]Blue skies"},
		{"text", "01 - Blue Skies.txt", "Blue skies\n"},
	} {
		candidate := got.Candidates[i]
		if candidate.Format != want.format || candidate.Label != want.label || candidate.Origin != "sidecar" || candidate.Language != "und" || string(candidate.Data) != want.data {
			t.Fatalf("candidate %d %+v", i, candidate)
		}
	}
}
