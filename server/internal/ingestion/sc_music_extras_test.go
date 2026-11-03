package ingestion

import "testing"

// A music album's own disc folder ("Bonus", "Extras", "Other") holds songs: the
// video extras rules do not apply to a music library.
func TestMusicDiscFoldersAreNotVideoExtras(t *testing.T) {
	for _, folder := range []string{"Bonus", "Extras", "Other", "Shorts"} {
		t.Run(folder, func(t *testing.T) {
			f := newLoopFixture(t, "music",
				"Artist/Album/01 - One.flac",
				"Artist/Album/"+folder+"/01 - Two.flac",
			)
			f.finish(f.queue())
			if songs := f.count(`SELECT count(*) FROM catalog_entities WHERE kind=7 AND retired=0`); songs != 2 {
				t.Fatalf("songs: %d; want 2: %q is an album disc, not a video extras folder", songs, folder)
			}
			if extras := f.count(`SELECT count(*) FROM item_extras`); extras != 0 {
				t.Fatalf("extras: %d; want 0 in a music library", extras)
			}
		})
	}
}
