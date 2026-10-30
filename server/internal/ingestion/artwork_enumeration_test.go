package ingestion

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/localmetadata"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// Procedural blank-media/image tree: real inventory and confined helper I/O,
// no FFmpeg/metadata provider or playback-availability claim.
func TestArtworkEnumerationUsesActualNamesStampsAndConfinedReads(t *testing.T) {
	root := t.TempDir()
	writeImage := func(relative string, n uint8) {
		t.Helper()
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rect(0, 0, 8, 12))
		img.SetRGBA(0, 0, color.RGBA{R: n, G: 255, A: 255})
		var body bytes.Buffer
		if err := png.Encode(&body, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for i, path := range []string{"Film/poster.png", "Film/poster-2.jpeg", "Film/Avatar (2009)-1.tbn", "Film/fanart-1.webp", "Film/clearlogo-2.png", "Film/banner.jpg", "Heroes/show.jpg", "Heroes/show-2.jpeg", "Heroes/art-1.tbn", "Heroes/Season 01/Season01a.png", "Heroes/Season 01/Season01b.tbn", "Heroes/Season 01/Season01-3.jpeg"} {
		writeImage(path, uint8(i+1))
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	scanner := &Service{storage: store}
	rootPage, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	source := catalog.LibrarySource{ID: "source", ResolvedPath: root, RootIdentity: rootPage.RootIdentity}
	capture := func(relative string) {
		t.Helper()
		page, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity, RelativePath: relative, Limit: 128})
		if err != nil {
			t.Fatal(err)
		}
		scanner.noteDirectoryPage("job", relative, "", page)
	}
	for _, dir := range []string{"Film", "Heroes", "Heroes/Season 01"} {
		capture(dir)
	}
	service, err := localmetadata.New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	var denied error
	guardCalls := 0
	read := func(kind, relative string, season int) ([]assets.LocalArtworkCandidate, bool) {
		ctx := storage.WithScanInput(context.Background(), store, source.ID, storage.InventoryRequest{Root: root, RootIdentity: source.RootIdentity})
		ctx = storage.WithScanReadGuard(ctx, func(context.Context, string) error { guardCalls++; return denied })
		ctx = localmetadata.WithArtworkListing(ctx, scanner.scanArtworkListed(ctx, "job", source))
		return service.ReadArtworkCandidates(ctx, "library", kind, root, filepath.Join(root, relative), season)
	}
	movie, complete := read("movie", "Film/Avatar (2009).mkv", -1)
	if !complete || len(movie) != 5 {
		t.Fatal("actual numbered/primary image members not retained", movie, complete)
	}
	counts := map[string]int{}
	keys := map[string]bool{}
	for _, v := range movie {
		counts[v.Slot]++
		keys[v.Key] = true
	}
	if !reflect.DeepEqual(counts, map[string]int{"item/poster": 3, "item/backdrop": 1, "item/logo": 1}) || len(keys) != 5 {
		t.Fatal("roles/images collapsed or inferred", counts, keys)
	}
	tv, complete := read("tv", "Heroes/Season 01/ep.mkv", 1)
	if !complete || len(tv) != 6 {
		t.Fatal("show/SeasonNN letter+number images missing", tv, complete)
	}
	counts = map[string]int{}
	for _, v := range tv {
		counts[v.Slot]++
	}
	if !reflect.DeepEqual(counts, map[string]int{"show/poster": 2, "show/backdrop": 1, "season/1/poster": 3}) {
		t.Fatal(counts)
	}
	// Cached image keys cannot skip the actual caller's current read guard.
	denied = errors.New("current analysis authority revoked")
	before := guardCalls
	if got, complete := read("movie", "Film/Avatar (2009).mkv", -1); complete || len(got) != 0 || guardCalls == before {
		t.Fatal("cache bypassed current authority", got, complete)
	}
	denied = nil
	// In-place image changes need fresh source observations even if the directory
	// itself did not change. A metadata stamp is not an immutable file version.
	writeImage("Film/poster-2.jpeg", 99)
	changed := filepath.Join(root, "Film/poster-2.jpeg")
	at := time.Now().Add(time.Second)
	if err := os.Chtimes(changed, at, at); err != nil {
		t.Fatal(err)
	}
	if got, complete := read("movie", "Film/Avatar (2009).mkv", -1); complete || len(got) != 0 {
		t.Fatal("stale image observation was published", got, complete)
	}
	capture("Film")
	fresh, complete := read("movie", "Film/Avatar (2009).mkv", -1)
	if !complete || len(fresh) != 5 {
		t.Fatal("fresh image observation failed", fresh, complete)
	}
	difference := 0
	for _, v := range fresh {
		if !keys[v.Key] {
			difference++
		}
	}
	if difference != 1 {
		t.Fatal("changed source image reused stale cached bytes", difference)
	}
	// An unknown/resumed directory is not a complete empty image set.
	if got, complete := read("movie", "Unknown/Film.mkv", -1); complete || len(got) != 0 {
		t.Fatal("unknown directory fabricated an empty observation", got, complete)
	}
}
