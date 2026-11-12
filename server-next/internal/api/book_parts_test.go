package api

import (
	"context"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// An audiobook in several files: each part has its own length and its place in the whole
// book, from the parts' stored lengths in order; from a part whose length is not known yet,
// no place is given.
func TestABookInPartsGivesEachPartItsPlace(t *testing.T) {
	s := newTestServer(t)
	owner := onMain(t, s, setup(t, s)).AccessToken
	ctx := context.Background()
	lib, err := s.db.CreateLibrary(ctx, "Audiobooks", db.LibraryAudiobooks, "en", []string{t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var files []db.ImportFile
	book := func(title string, lengths ...int64) db.ImportItem {
		var parts []db.ImportPart
		for n, ms := range lengths {
			ref := db.FileRef{Name: title + string(rune('a'+n)) + ".mp3"}
			files = append(files, db.ImportFile{FileRef: ref, Role: db.FileMedia, Size: 1, ModifiedNS: 1, Container: "mp3", DurationMS: ms,
				Streams: []db.Stream{{Kind: db.StreamAudio, Codec: "mp3", Channels: 2, SampleRate: 44100}}})
			parts = append(parts, db.ImportPart{File: ref})
		}
		return db.ImportItem{Kind: db.KindAudiobook, Keys: []db.ItemKey{{Kind: db.KeyName, Key: "audiobook|" + title}}, Title: title,
			Versions: []db.ImportVersion{{Parts: parts}}}
	}
	items := []db.ImportItem{book("Whole", 600_000, 900_000, 300_000), book("Unread", 600_000, 0, 300_000), book("Single", 600_000)}
	if _, err := s.db.Import(ctx, db.ImportBatch{LibraryID: lib.ID, PathID: lib.Paths[0].ID, Scan: 1, Dirs: []db.ImportDir{{}}, Files: files, Items: items}); err != nil {
		t.Fatal(err)
	}
	places := func(title string) (offsets, lengths []int64) {
		t.Helper()
		for _, it := range get[ItemPage](t, s, owner, "/v1/items?library="+lib.PublicID).Items {
			if it.Title != title {
				continue
			}
			for _, p := range get[Item](t, s, owner, "/v1/items/"+it.ID).Versions[0].Parts {
				offset, length := int64(-1), int64(-1)
				if p.OffsetMs != nil {
					offset = *p.OffsetMs
				}
				if p.DurationMs != nil {
					length = *p.DurationMs
				}
				offsets, lengths = append(offsets, offset), append(lengths, length)
			}
		}
		return
	}
	if offsets, lengths := places("Whole"); !slices.Equal(offsets, []int64{0, 600_000, 1_500_000}) || !slices.Equal(lengths, []int64{600_000, 900_000, 300_000}) {
		t.Errorf("a book in three parts: offsets %v, lengths %v", offsets, lengths)
	}
	if offsets, lengths := places("Unread"); !slices.Equal(offsets, []int64{0, -1, -1}) || !slices.Equal(lengths, []int64{600_000, -1, 300_000}) {
		t.Errorf("a book whose second part is not probed yet: offsets %v, lengths %v", offsets, lengths)
	}
	if offsets, _ := places("Single"); !slices.Equal(offsets, []int64{-1}) {
		t.Errorf("a book in one file has offsets %v", offsets)
	}
}
