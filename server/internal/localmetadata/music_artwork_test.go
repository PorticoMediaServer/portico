package localmetadata

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"path/filepath"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func musicImageFixture(t *testing.T, extension string, red uint8) []byte {
	t.Helper()
	if extension == "webp" {
		// Existing procedural 2x2 WebP fixture; no album artwork is copied.
		data, err := base64.StdEncoding.DecodeString("UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoCAAIAAgA0JaACdLoB+AADsAD+8Oj3/yC5YXXI1/8gP+QH/ID/+PIAAAA=")
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			img.SetRGBA(x, y, color.RGBA{R: red, B: 100, A: 255})
		}
	}
	var out bytes.Buffer
	var err error
	if extension == "png" {
		err = png.Encode(&out, img)
	} else {
		err = jpeg.Encode(&out, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestMusicArtworkPlexAliasesAndActualImageFormats(t *testing.T) {
	roles := []struct {
		role, directory string
		stems           []string
	}{
		{"artist/portrait", "/music/Pink Floyd", []string{"artist", "artist-cover", "artist-default", "artist-folder", "artist-poster", "cover", "default", "folder", "poster"}},
		{"artist/backdrop", "/music/Pink Floyd", []string{"art", "artist-art", "artist-backdrop", "artist-background", "artist-fanart", "backdrop", "background", "fanart"}},
		{"album/cover", "/music/Pink Floyd/The Wall", []string{"album", "cover", "default", "folder", "poster"}},
		{"album/backdrop", "/music/Pink Floyd/The Wall", []string{"art", "backdrop", "background", "fanart"}},
	}
	for _, role := range roles {
		for _, stem := range role.stems {
			for _, extension := range []string{"jpg", "jpeg", "png", "tbn", "webp"} {
				t.Run(role.role+"/"+stem+"."+extension, func(t *testing.T) {
					service, err := New(t.TempDir(), "", nil)
					if err != nil {
						t.Fatal(err)
					}
					wanted := filepath.Join(role.directory, stem+"."+extension)
					reads := 0
					service.readSmall = func(_ context.Context, _ string, path string, limit int64) ([]byte, error) {
						if !strings.HasPrefix(path, "/music/") {
							t.Fatal("art lookup escaped source root", path)
						}
						if path == wanted {
							reads++
							return musicImageFixture(t, extension, 50), nil
						}
						return nil, errMissing
					}
					got := service.ReadSidecars(context.Background(), "music", "music", "/music", "/music/Pink Floyd/The Wall/101 - In the Flesh.flac", -1)
					if got[role.role] == "" || reads == 0 {
						t.Fatal("Plex image alias was not used", wanted, got)
					}
					if role.role == "album/cover" {
						if got["artist/portrait"] != "" {
							t.Fatal("album cover became artist portrait", got)
						}
						facts := service.PrepareKind(context.Background(), "music", "music", "/music", "/music/Pink Floyd/The Wall/101 - In the Flesh.flac", assets.Facts{})
						if facts.ArtworkKey == "" {
							t.Fatal("album cover did not enter normal audio facts", wanted)
						}
					}
					file, mime, err := service.Open(got[role.role])
					if err != nil {
						t.Fatal(err)
					}
					file.Close()
					expected := "image/jpeg"
					if extension == "png" {
						expected = "image/png"
					} else if extension == "webp" {
						expected = "image/webp"
					}
					if mime != expected {
						t.Fatal("image encoding was inferred from suffix", mime, expected)
					}
				})
			}
		}
	}
}

func TestMusicArtworkDiscFoldersAndAlbumBackgroundPrecedence(t *testing.T) {
	service, err := New(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	images := map[string][]byte{
		"/music/Pink Floyd/artist-poster.jpg":     musicImageFixture(t, "jpg", 20),
		"/music/Pink Floyd/artist-background.jpg": musicImageFixture(t, "jpg", 40),
		"/music/Pink Floyd/The Wall/cover.png":    musicImageFixture(t, "png", 60),
		"/artist-poster.jpg":                      musicImageFixture(t, "jpg", 80),
	}
	service.readSmall = func(_ context.Context, _ string, path string, _ int64) ([]byte, error) {
		if !strings.HasPrefix(path, "/music/") {
			t.Fatal("art lookup escaped source root", path)
		}
		if data, ok := images[path]; ok {
			return data, nil
		}
		return nil, errMissing
	}
	read := func() map[string]string {
		return service.ReadSidecars(context.Background(), "music", "music", "/music", "/music/Pink Floyd/The Wall/CD1/01 - In the Flesh.flac", -1)
	}
	got := read()
	if got["artist/portrait"] == "" || got["album/cover"] == "" || got["artist/backdrop"] == "" || got["album/backdrop"] != got["artist/backdrop"] {
		t.Fatal("disc/artist fallback missing", got)
	}
	images["/music/Pink Floyd/The Wall/background.png"] = musicImageFixture(t, "png", 100)
	got = read()
	if got["album/backdrop"] == "" || got["album/backdrop"] == got["artist/backdrop"] {
		t.Fatal("artist background displaced album's own", got)
	}
}
