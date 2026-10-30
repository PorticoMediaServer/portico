package ingestion

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Blank media and procedural artwork exercise real confined helper reads.
// This proves local image discovery/custody, not a decoded video or scan grant.
func TestPrimaryVideoArtAliasesUseConfinedInventoryReader(t *testing.T) {
	root := t.TempDir()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 8, 12))); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Avatar (2009).mkv", "cover.tbn", "Heroes/Season 01/Heroes S01E01.mkv", "Heroes/show.jpeg", "Heroes/Season 01/Season01.webp"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		body := imageBytes.Bytes()
		if filepath.Ext(name) == ".mkv" {
			body = nil
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside.png")
	if err := os.WriteFile(outside, imageBytes.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "Heroes", "clearlogo.png")); err != nil {
		t.Fatal(err)
	}
	page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	service, err := localmetadata.New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := storage.WithScanInput(context.Background(), store, "fixture-source", storage.InventoryRequest{Root: root, RootIdentity: page.RootIdentity})
	movie := service.MovieArtwork(ctx, "fixture-library", filepath.Join(root, "Avatar (2009).mkv"), assets.Facts{})
	if movie.ArtworkKey == "" {
		t.Fatal("actual .tbn image alias missing through confined helper")
	}
	tv := service.ReadSidecars(ctx, "fixture-library", "tv", root, filepath.Join(root, "Heroes/Season 01/Heroes S01E01.mkv"), 1)
	if tv["show/poster"] == "" || tv["season/1/poster"] == "" {
		t.Fatal("actual show/SeasonNN aliases missing", tv)
	}
	if tv["show/logo"] != "" {
		t.Fatal("art alias escaped admitted root through symlink")
	}
}
