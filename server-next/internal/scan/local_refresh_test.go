package scan_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// Both the explicit action and a scanner reread must update unlocked fields even when
// the title is locked, without replacing a chosen cover or the reader's state.
func TestReadingMetadataRereadsRespectEachLock(t *testing.T) {
	for _, action := range []string{"refresh", "scan"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, db.LibraryBooks)
			f.s.Pictures = pictures{}
			// The PDF was added first; refresh still reads the later EPUB's embedded facts.
			f.put("book.pdf", []byte("a PDF rendition"))
			f.put("book.jpg", picture(t, 10))
			f.scan()
			id := f.titles(db.KindBook)["book"].PublicID
			f.put("book.epub", epub(t, "3.0", `<dc:title>First</dc:title><dc:creator>Old Author</dc:creator><dc:description>Old description</dc:description>`))
			f.scan()
			locks, err := f.d.MetadataLocks(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			keptTitle := "Owner title"
			if _, err := f.d.EditMetadata(ctx, id, locks.Revision, db.MetadataEdit{Title: &keptTitle}); err != nil {
				t.Fatal(err)
			}
			f.watch(id)
			before := f.detail(id)
			reread := func() {
				t.Helper()
				if action == "scan" {
					f.scan()
				} else if err := f.s.RefreshMetadata(ctx, id); err != nil {
					t.Fatal(err)
				}
			}
			f.put("book.epub", epub(t, "3.0", `<dc:title>Second</dc:title><dc:creator>New Author</dc:creator><dc:description>New description</dc:description>
				<meta name="calibre:series" content="New Series"/><meta name="calibre:series_index" content="7"/>`))
			f.put("book.jpg", picture(t, 20))
			reread()
			after := f.detail(id)
			if after.Title != keptTitle || after.Summary != "New description" || !reflect.DeepEqual(creditNames(after), []string{"New Author"}) ||
				after.IndexNumber == nil || *after.IndexNumber != 7 || f.titles(db.KindSeries)["New Series"].PublicID == "" {
				t.Fatalf("unlocked details not reread: title %q, summary %q, authors %v, index %v", after.Title, after.Summary, creditNames(after), after.IndexNumber)
			}
			cover := shown(after.Item, db.ImageCover)
			if cover == nil || cover.FileHash == shown(before.Item, db.ImageCover).FileHash {
				t.Fatal("the unlocked cover was not reread")
			}
			if action == "refresh" && !reflect.DeepEqual(before.Versions, after.Versions) || !after.State.Watched {
				t.Fatal("metadata refresh changed versions or the reader's state")
			}
			candidates, err := f.d.Pictures(ctx, id, db.ImageCover)
			if err != nil || len(candidates) == 0 {
				t.Fatalf("cover candidates: %v, %v", candidates, err)
			}
			if err := f.d.ChoosePicture(ctx, id, db.ImageCover, candidates[0].ID); err != nil {
				t.Fatal(err)
			}
			f.put("book.jpg", picture(t, 30))
			f.put("book.epub", epub(t, "3.0", `<dc:description>Another description</dc:description>`))
			reread()
			if d := f.detail(id); shown(d.Item, db.ImageCover).FileHash != cover.FileHash || d.Summary != "Another description" {
				t.Fatal("reread replaced a chosen cover, or skipped an unlocked description")
			}
			if action == "refresh" {
				f.put("book.epub", []byte("a truncated upload"))
				f.remove("book.pdf")
				if err := f.s.RefreshMetadata(ctx, id); !errors.Is(err, scan.ErrMetadataUnreadable) {
					t.Fatalf("unreadable file: %v", err)
				}
				if f.detail(id).Summary != "Another description" {
					t.Fatal("unreadable metadata erased the previous description")
				}
			}
		})
	}
}

func TestRefreshingAComicChangesOnlyItsSeriesAndNumber(t *testing.T) {
	f := newFixture(t, db.LibraryComics)
	f.s.Pictures = pictures{}
	comic := func(title, series, number string, width int) []byte {
		return zipped(t, [2]string{"001.png", string(picture(t, width))}, [2]string{"ComicInfo.xml", `<ComicInfo><Title>` + title + `</Title>
			<Series>` + series + `</Series><Number>` + number + `</Number><Summary>A story.</Summary></ComicInfo>`})
	}
	f.put("Old/001.cbz", comic("One", "Old", "1", 10))
	f.put("Old/002.cbz", comic("Two", "Old", "2", 12))
	f.scan()
	old := f.titles(db.KindSeries)["Old"].PublicID
	one, two := f.children(old)[1].PublicID, f.children(old)[2].PublicID
	before := f.detail(two)
	f.put("Old/001.cbz", comic("Revised", "New", "9", 20))
	if err := f.s.RefreshMetadata(ctx, one); err != nil {
		t.Fatal(err)
	}
	newSeries := f.titles(db.KindSeries)["New"].PublicID
	if newSeries == "" || f.children(newSeries)[9].PublicID != one || f.children(old)[2].PublicID != two {
		t.Fatalf("series %v, new %q children %+v; old children %+v", keys(f.titles(db.KindSeries)), newSeries, f.children(newSeries), f.children(old))
	}
	if after := f.detail(two); !reflect.DeepEqual(before, after) {
		t.Fatal("refresh changed the neighbouring issue")
	}
	if d := f.detail(one); d.Title != "Revised" || shown(d.Item, db.ImageCover).Width != 20 {
		t.Fatalf("comic metadata or cover not reread: %+v", d.Item)
	}
}

func TestRefreshingAnAudiobookReadsItsSidecarsAboveDiscs(t *testing.T) {
	f := newFixture(t, db.LibraryAudiobooks)
	f.s.Pictures = pictures{}
	f.tagged("Author/Book/Disc 1/01.mp3", "TALB", "Tag title", "TPE1", "Tag author")
	f.tagged("Author/Book/Disc 2/01.mp3", "TALB", "Tag title", "TPE1", "Tag author")
	f.text("Author/Book/metadata.opf", `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata>
		<dc:title>Original</dc:title><dc:creator>Original Author</dc:creator></metadata></package>`)
	f.put("Author/Book/cover.jpg", picture(t, 10))
	f.scan()
	id := f.titles(db.KindAudiobook)["Original"].PublicID
	locks, err := f.d.MetadataLocks(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.d.EditMetadata(ctx, id, locks.Revision, db.MetadataEdit{Lock: []string{db.FieldCredits}}); err != nil {
		t.Fatal(err)
	}
	before := f.detail(id)
	f.text("Author/Book/metadata.opf", `<package xmlns:dc="http://purl.org/dc/elements/1.1/"><metadata><dc:title>Revised</dc:title>
		<dc:creator>Changed Author</dc:creator><dc:description>Revised description</dc:description>
		<meta name="calibre:series" content="Revised Series"/><meta name="calibre:series_index" content="4"/></metadata></package>`)
	f.put("Author/Book/cover.jpg", picture(t, 20))
	for range 2 {
		if err := f.s.RefreshMetadata(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	d := f.detail(id)
	if d.Title != "Revised" || d.Summary != "Revised description" || !reflect.DeepEqual(creditNames(before), creditNames(d)) ||
		d.IndexNumber == nil || *d.IndexNumber != 4 || f.titles(db.KindSeries)["Revised Series"].PublicID == "" {
		t.Fatalf("audiobook details: title %q, summary %q, credits %v, index %v", d.Title, d.Summary, creditNames(d), d.IndexNumber)
	}
	if !reflect.DeepEqual(before.Versions, d.Versions) || len(d.Versions[0].Parts) != 2 {
		t.Fatal("metadata refresh rewrote the audiobook's discs")
	}
	covers, err := f.d.Pictures(ctx, id, db.ImagePoster)
	if err != nil || len(covers) != 1 || covers[0].Width != 20 {
		t.Fatalf("repeated refresh duplicated or lost the cover: %+v, %v", covers, err)
	}
}
