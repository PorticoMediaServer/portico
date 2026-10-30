package localmetadata

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "--portico-storage-helper" {
		if err := storage.Helper(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func TestArtworkEnumerationFailedOldReaderCannotPoisonNewStampCache(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "poster-1.png")
	write := func(n uint8) {
		t.Helper()
		img := image.NewRGBA(image.Rect(0, 0, 8, 12))
		img.SetRGBA(0, 0, color.RGBA{R: n, G: 255, A: 255})
		var raw bytes.Buffer
		if err := png.Encode(&raw, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(1)
	rootPage, err := storage.LocalInventoryPage(storage.InventoryRequest{Root: root, RelativePath: ".", Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	request := storage.InventoryRequest{Root: root, RootIdentity: rootPage.RootIdentity, RelativePath: "poster-1.png"}
	old, err := storage.LocalInventoryStat(request)
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	store := storage.New(binary)
	service, err := New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	firstBody := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var reads atomic.Int32
	service.readSmall = func(ctx context.Context, library, path string, limit int64) ([]byte, error) {
		raw, err := store.ReadSmall(ctx, library, path, limit)
		if reads.Add(1) == 1 {
			close(firstBody)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return raw, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	read := func(stamp string) ([]assets.LocalArtworkCandidate, bool) {
		rooted := storage.WithScanInput(ctx, store, "source", storage.InventoryRequest{Root: root, RootIdentity: rootPage.RootIdentity})
		rooted = WithArtworkListing(rooted, func(string) (ArtworkDirectoryListing, bool) {
			return ArtworkDirectoryListing{SourceID: "source", RootIdentity: rootPage.RootIdentity, Entries: []ArtworkListingEntry{{Name: "poster-1.png", Revision: stamp}}}, true
		})
		return service.ReadArtworkCandidates(rooted, "library", "movie", root, filepath.Join(root, "Film.mkv"), -1)
	}
	oldDone := make(chan bool, 1)
	go func() { _, complete := read(storage.InventoryRevision(old)); oldDone <- complete }()
	select {
	case <-firstBody:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	write(2)
	at := time.Now().Add(time.Second)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	current, err := storage.LocalInventoryStat(request)
	if err != nil {
		t.Fatal(err)
	}
	fresh, complete := read(storage.InventoryRevision(current))
	if !complete || len(fresh) != 1 {
		t.Fatal("new observed image failed", fresh, complete)
	}
	if fresh[0].SourceName != "poster-1.png" || fresh[0].SourceRank != 1025 {
		t.Fatal("admitted basename/rank lost", fresh[0])
	}
	unblock()
	select {
	case complete := <-oldDone:
		if complete {
			t.Fatal("old image published after source change")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	again, complete := read(storage.InventoryRevision(current))
	if !complete || len(again) != 1 || again[0] != fresh[0] {
		t.Fatal("failed old reader poisoned current image cache", fresh, again, complete)
	}
}

func TestArtworkEnumerationRetainsCanonicalNameAfterEarlierAlternate(t *testing.T) {
	root := t.TempDir()
	for i, name := range []string{"poster-7.png", "poster.jpg"} {
		img := image.NewRGBA(image.Rect(0, 0, 8, 12))
		img.SetRGBA(0, 0, color.RGBA{R: uint8(i + 1), G: 255, A: 255})
		var raw bytes.Buffer
		if err := png.Encode(&raw, img); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), raw.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
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
	s, err := New(t.TempDir(), "", store)
	if err != nil {
		t.Fatal(err)
	}
	ctx := storage.WithScanInput(context.Background(), store, "source", storage.InventoryRequest{Root: root, RootIdentity: page.RootIdentity})
	ctx = WithArtworkListing(ctx, func(string) (ArtworkDirectoryListing, bool) {
		listing := ArtworkDirectoryListing{SourceID: "source", RootIdentity: page.RootIdentity}
		for _, entry := range page.Entries {
			listing.Entries = append(listing.Entries, ArtworkListingEntry{Name: entry.Name, Revision: storage.InventoryRevision(entry)})
		}
		return listing, true
	})
	values, complete := s.ReadArtworkCandidates(ctx, "library", "movie", root, filepath.Join(root, "Film.mkv"), -1)
	if !complete || len(values) != 2 || values[0].SourceName != "poster.jpg" || values[0].SourceRank != 16 || values[1].SourceName != "poster-7.png" || values[1].SourceRank != 1031 || values[0].Key == values[1].Key {
		t.Fatal("actual admitted image names/ranks not retained in canonical order", values, complete)
	}
}
