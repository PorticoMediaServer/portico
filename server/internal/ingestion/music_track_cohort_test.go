package ingestion

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestMusicTrackCohortRequiresCompleteCurrentDirectoryObservation(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "Album")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	source := catalog.LibrarySource{ResolvedPath: root}
	s := New(nil, nil, assets.Probe{})
	page := func(complete bool) storage.InventoryPage {
		snapshot, err := storage.LocalInventoryDirectory(storage.InventoryRequest{Root: root, RelativePath: "Album"})
		if err != nil {
			t.Fatal(err)
		}
		return storage.InventoryPage{Complete: complete, DirectoryIdentity: snapshot.ObjectIdentity, DirectoryRevision: storage.InventoryRevision(snapshot), Entries: []storage.Snapshot{{Name: "101 - One.flac"}, {Name: "102 - Two.flac"}, {Name: "201 - Three.flac"}, {Name: "cover.jpg"}}}
	}
	partial := page(false)
	partial.Entries = partial.Entries[:1]
	s.noteDirectoryPage("job", "Album", "", partial)
	if _, known := s.scanMusicTracksListed(context.Background(), "job", source)(directory); known {
		t.Fatal("partial listing trusted")
	}
	remaining := page(true)
	remaining.Entries = remaining.Entries[1:]
	s.noteDirectoryPage("job", "Album", "next", remaining)
	names, known := s.scanMusicTracksListed(context.Background(), "job", source)(directory)
	if !known || len(names) != 3 {
		t.Fatal(names, known)
	}
	if _, known := s.scanMusicTracksListed(context.Background(), "job", source)(filepath.Join(root, "..")); known {
		t.Fatal("outside root trusted")
	}
	duplicate := page(true)
	duplicate.Entries = append(duplicate.Entries, storage.Snapshot{Name: "101 - ONE.flac"})
	s.noteDirectoryPage("job", "Album", "", duplicate)
	if _, known := s.scanMusicTracksListed(context.Background(), "job", source)(directory); known {
		t.Fatal("case-colliding actual names collapsed into a clear cohort")
	}
	s.noteDirectoryPage("job", "Album", "", page(true))
	changed := time.Now().Add(time.Hour)
	if err := os.Chtimes(directory, changed, changed); err != nil {
		t.Fatal(err)
	}
	if _, known := s.scanMusicTracksListed(context.Background(), "job", source)(directory); known {
		t.Fatal("changed directory trusted")
	}
}
