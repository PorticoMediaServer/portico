package localmetadata

import (
	"context"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestVideoArtworkCandidateSlots(t *testing.T) {
	for _, row := range []struct {
		kind, media, path, slot string
		season                  int
	}{
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/poster-2.png", "item/poster", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/Avatar (2009)-1.tbn", "item/poster", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/fanart-1.jpeg", "item/backdrop", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/clearlogo-2.webp", "item/logo", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Other/poster-2.png", "", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/banner.jpg", "", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/square.jpg", "", -1},
		{"movie", "/root/Film/Avatar (2009).mkv", "/root/Film/poster-two.png", "", -1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/show-2.jpeg", "show/poster", 1},
		{"anime", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/background-1.webp", "show/backdrop", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/Season01a.png", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/Season01b.tbn", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/Season01-3.jpeg", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/season01-1.jpg", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/poster-2.png", "season/1/poster", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/Season02a.png", "", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/poster-2.jpg", "", 1},
		{"tv", "/root/Heroes/Season 01/ep.mkv", "/root/Heroes/Season 01/ep-thumb.png", "", 1},
	} {
		if got := videoArtworkSlot(row.kind, "/root", row.media, row.path, row.season); got != row.slot {
			t.Errorf("%s: %s want %s", row.path, got, row.slot)
		}
	}
}

func TestArtworkEnumerationCanceledEmptyListingStaysUnknown(t *testing.T) {
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(t.TempDir(), "", storage.New(binary))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ctx = WithArtworkListing(ctx, func(string) (ArtworkDirectoryListing, bool) {
		return ArtworkDirectoryListing{SourceID: "source", RootIdentity: "root"}, true
	})
	if got, complete := service.ReadArtworkCandidates(ctx, "library", "movie", "/root", "/root/Film.mkv", -1); complete || len(got) != 0 {
		t.Fatal("cancelled source invented complete empty image observation", got, complete)
	}
}
