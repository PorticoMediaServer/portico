package localmetadata

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

func TestMoviePrimaryArtworkAliasesAndImageSuffixes(t *testing.T) {
	image := sidecarPNG(t) // Procedural PNG; suffix selection never substitutes for content checks.
	for _, stem := range []string{"Avatar (2009)-poster", "Avatar (2009)", "poster", "folder", "cover", "default", "movie"} {
		for _, extension := range []string{".jpg", ".jpeg", ".png", ".tbn", ".webp"} {
			name := stem + extension
			t.Run(name, func(t *testing.T) {
				service, err := New(t.TempDir(), "", nil)
				if err != nil {
					t.Fatal(err)
				}
				candidate := filepath.Join("/root/Avatar (2009)", name)
				service.readSmall = func(_ context.Context, library, path string, limit int64) ([]byte, error) {
					if library != "lib" || limit != 4<<20 {
						t.Fatal("sidecar read lost its authority/bound", library, limit)
					}
					if path == candidate {
						return image, nil
					}
					return nil, os.ErrNotExist
				}
				got := service.MovieArtwork(context.Background(), "lib", "/root/Avatar (2009)/Avatar (2009).mkv", assets.Facts{})
				if got.ArtworkKey == "" {
					t.Fatal("actual image alias not read", candidate)
				}
			})
		}
	}
}

func TestVideoShowSeasonBackdropAndLogoAliases(t *testing.T) {
	image := sidecarPNG(t)
	rows := []struct {
		kind, path, candidate, role string
		season                      int
	}{
		{"movie", "/root/Film/Film.mkv", "/root/Film/art.jpeg", "item/backdrop", -1},
		{"movie", "/root/Film/Film.mkv", "/root/Film/background.webp", "item/backdrop", -1},
		{"movie", "/root/Film/Film.mkv", "/root/Film/Film-fanart.tbn", "item/backdrop", -1},
		{"movie", "/root/Film/Film.mkv", "/root/Film/clearlogo.jpg", "item/logo", -1},
		{"movie", "/root/Film/Film.mkv", "/root/Film/Film-clearlogo.png", "item/logo", -1},
		{"tv", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/show.jpg", "show/poster", 1},
		{"anime", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/background.png", "show/backdrop", 1},
		{"tv", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/art.tbn", "show/backdrop", 1},
		{"tv", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/clearlogo.webp", "show/logo", 1},
		{"tv", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/Season 1/Season01.jpeg", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 1/Heroes S01E01.mkv", "/root/Heroes/season01.tbn", "season/1/poster", 1},
		{"tv", "/root/Heroes/Specials/Heroes S00E01.mkv", "/root/Heroes/season-specials-poster.webp", "season/0/poster", 0},
	}
	for _, row := range rows {
		t.Run(row.candidate, func(t *testing.T) {
			service, err := New(t.TempDir(), "", nil)
			if err != nil {
				t.Fatal(err)
			}
			service.readSmall = func(_ context.Context, _ string, path string, _ int64) ([]byte, error) {
				if path == row.candidate {
					return image, nil
				}
				return nil, os.ErrNotExist
			}
			got := service.ReadSidecars(context.Background(), "lib", row.kind, "/root", row.path, row.season)
			if got[row.role] == "" {
				t.Fatalf("alias not retained for actual scope/role %s: %v", row.role, got)
			}
		})
	}
}

func TestVideoArtworkAliasesDoNotAcceptImageSuffixAsEvidence(t *testing.T) {
	service, err := New(t.TempDir(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	service.readSmall = func(_ context.Context, _ string, _ string, _ int64) ([]byte, error) {
		return []byte("not an image"), nil
	}
	got := service.MovieArtwork(context.Background(), "lib", "/root/Film.mkv", assets.Facts{})
	if got.ArtworkKey != "" {
		t.Fatal("image suffix granted invalid content")
	}
}
