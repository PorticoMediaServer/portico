package catalog

import (
	"context"
	"encoding/hex"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
)

// Container roots publish no metadata head. Their admission must use the full
// artwork incarnation that lives with the entity, so a real album opens and its
// opening is fenced by the same lifetime the catalogue deletes.
func TestFileInfoAdmissionContainerRootUsesArtworkIncarnation(t *testing.T) {
	c := catalogtest.Open(t)
	music := c.Library("music", "Music", "music", "/music")
	artist := c.Artist(music, "Artist")
	album := c.Album(artist, "Album", 2020)
	c.Song(album, 1, "/music/one.mp4", "Song")
	c.Drain()

	var metadataHeads int
	if err := c.DB.QueryRow(`SELECT count(*) FROM metadata_publication_heads WHERE item_id=?`, album.ID).Scan(&metadataHeads); err != nil {
		t.Fatal(err)
	}
	if metadataHeads != 0 {
		t.Fatalf("album unexpectedly has a metadata head: %d", metadataHeads)
	}
	var stored string
	if err := c.DB.QueryRow(`SELECT incarnation FROM artwork_entity_heads WHERE kind='album' AND entity_id=?`, album.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	raw, err := hex.DecodeString(stored)
	if err != nil || len(raw) != 16 {
		t.Fatalf("artwork incarnation %q: %v", stored, err)
	}
	var want [16]byte
	copy(want[:], raw)

	svc := New(c.DB)
	viewer := Viewer{Profile: "p", Fence: "authority", Libraries: []string{"music"}}
	ad, err := svc.CaptureFileInfoAdmission(context.Background(), viewer, "server", album.Public, true, "actor", false)
	if err != nil {
		t.Fatal(err)
	}
	if ad.ItemIncarnation != want {
		t.Fatalf("admission incarnation %x, artwork head %x", ad.ItemIncarnation, want)
	}
	if ad.Kind != "album" || !ad.Children || ad.Playable {
		t.Fatalf("album admission kind=%q children=%v playable=%v", ad.Kind, ad.Children, ad.Playable)
	}
}
