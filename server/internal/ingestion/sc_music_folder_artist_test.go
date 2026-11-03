package ingestion

import "testing"

// A music library with no tags (or with album tags only) is still laid out as
// Artist/Album/track. The album name already comes from the folder, so the
// artist does too; otherwise every album of the library piles up under one
// "Unknown artist".
func TestMusicFolderArtistIsUsedWithoutArtistTags(t *testing.T) {
	f := newLoopFixture(t, "music",
		"Artist/Album/01 - One.flac",
		"Other Artist/Other Album/01 - Two.flac",
	)
	f.finish(f.queue())
	if artists := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=5 AND retired=0`); artists != 2 {
		t.Fatalf("artists: %d; want 2 (one per album folder)", artists)
	}
	for album, artist := range map[string]string{"Album": "Artist", "Other Album": "Other Artist"} {
		var got string
		if err := f.db.QueryRow(`SELECT ar.title FROM catalog_entities a JOIN catalog_albums d ON d.entity_id=a.id JOIN catalog_entities ar ON ar.id=d.artist_id WHERE a.kind=6 AND a.title=?`, album).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != artist {
			t.Fatalf("album %q artist = %q; want %q from its folder", album, got, artist)
		}
	}
}
