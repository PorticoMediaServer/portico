package scan_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/reading"
)

func TestArtistFolderArtworkIsReadOnScanAllFiles(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	f.tagged("Air/Moon Safari/01.mp3", "TIT2", "La Femme d'Argent", "TPE1", "Air", "TALB", "Moon Safari")
	f.put("Air/folder.jpg", picture(t, 11))
	f.put("Air/fanart.jpg", picture(t, 22))
	f.put("Air/logo.png", picture(t, 33))
	f.put("Air/Moon Safari/cover.jpg", picture(t, 44))
	f.scan() // An existing library whose artwork was never imported.
	f.s.Pictures, f.s.AllFiles = pictures{}, true
	if stats := f.scan(); stats.ItemsNew != 0 {
		t.Fatalf("Scan all files added %d titles", stats.ItemsNew)
	}
	artist := f.titles(db.KindArtist)["Air"]
	for role, width := range map[db.ImageRole]int{db.ImagePortrait: 11, db.ImageBackdrop: 22, db.ImageLogo: 33} {
		if image := shown(artist, role); image == nil || image.Width != width {
			t.Errorf("artist role %v: %+v", role, image)
		}
	}
	if image := shown(f.titles(db.KindAlbum)["Moon Safari"], db.ImagePoster); image == nil || image.Width != 44 {
		t.Fatalf("album lost its own cover: %+v", image)
	}
	f.s.AllFiles = false
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("unchanged scan wrote %d rows", stats.RowsWritten)
	}
	f.put("Air/folder.jpg", picture(t, 12))
	f.scan()
	if image := shown(f.titles(db.KindArtist)["Air"], db.ImagePortrait); image == nil || image.Width != 12 {
		t.Fatalf("changed portrait: %+v", image)
	}
}

func TestBookCoversPreferSidecarsToBoundedEPUBArtwork(t *testing.T) {
	f := newFixture(t, db.LibraryBooks)
	container := `<container><rootfiles><rootfile full-path="OEBPS/book.opf"/></rootfiles></container>`
	pkg := `<package><metadata><title>Harbour</title></metadata><manifest>
		<item id="jacket" href="images/cover.png" properties="cover-image"/></manifest></package>`
	inside := picture(t, 15)
	f.put("Harbour/book.epub", zipped(t, [2]string{"META-INF/container.xml", container},
		[2]string{"OEBPS/book.opf", pkg}, [2]string{"OEBPS/images/cover.png", string(inside)}))
	// EPUB 2 names the cover by its manifest id instead of the cover-image property.
	legacy := strings.Replace(pkg, "<title>Harbour</title>", `<title>Old Harbour</title><meta name="cover" content="jacket"/>`, 1)
	legacy = strings.Replace(legacy, ` properties="cover-image"`, "", 1)
	f.put("Old/book.epub", zipped(t, [2]string{"META-INF/container.xml", container},
		[2]string{"OEBPS/book.opf", legacy}, [2]string{"OEBPS/images/cover.png", string(inside)}))
	f.put("PDF/edition.pdf", []byte("%PDF-1.4"))
	f.put("PDF/cover.jpg", picture(t, 25))
	f.scan()
	before := f.titles(db.KindBook)["Harbour"]
	f.s.Pictures, f.s.AllFiles = pictures{}, true
	if stats := f.scan(); stats.ItemsNew != 0 {
		t.Fatalf("Scan all files added %d books", stats.ItemsNew)
	}
	books := f.titles(db.KindBook)
	if books["Harbour"].PublicID != before.PublicID {
		t.Fatal("the existing book was replaced")
	}
	for _, name := range []string{"Harbour", "Old Harbour"} {
		if image := shown(books[name], db.ImageCover); image == nil || image.Width != 15 {
			t.Fatalf("%s embedded cover: %+v", name, books[name].Images)
		}
	}
	if image := shown(books["edition"], db.ImageCover); image == nil || image.Width != 25 {
		t.Fatalf("PDF local cover: %+v", books["edition"].Images)
	}
	f.s.AllFiles = false
	f.put("Harbour/cover.jpg", picture(t, 35))
	f.scan()
	if image := shown(f.titles(db.KindBook)["Harbour"], db.ImageCover); image == nil || image.Width != 35 {
		t.Fatalf("local cover did not win: %+v", image)
	}
	f.remove("Harbour/cover.jpg")
	f.scan()
	book := f.titles(db.KindBook)["Harbour"]
	if image := shown(book, db.ImageCover); image == nil || image.Width != 15 {
		t.Fatalf("removed sidecar did not fall back: %+v", book.Images)
	}
	if candidates, err := f.d.Pictures(ctx, book.PublicID, db.ImageCover); err != nil || len(candidates) != 1 || candidates[0].Source != db.ImageFromEmbedded {
		t.Fatalf("re-reading added duplicate covers: %+v, %v", candidates, err)
	}
}

func TestComicSeriesAndIssuesUseLocalCoversThenFirstPage(t *testing.T) {
	f := newFixture(t, db.LibraryComics)
	f.s.Pictures = pictures{}
	f.put("Saga/cover.jpg", picture(t, 10))
	f.put("Saga/Saga 001-poster.jpg", picture(t, 20))
	f.put("Saga/Saga 001.cbz", zipped(t, [2]string{"10.png", string(picture(t, 40))},
		[2]string{"2.png", string(picture(t, 30))}, [2]string{".hidden/1.png", string(picture(t, 50))}))
	f.scan()
	series, issue := f.titles(db.KindSeries)["Saga"], f.titles(db.KindIssue)["Saga 001"]
	if image := shown(series, db.ImageCover); image == nil || image.Width != 10 {
		t.Fatalf("series local cover: %+v", series.Images)
	}
	if image := shown(issue, db.ImageCover); image == nil || image.Width != 20 {
		t.Fatalf("issue local cover: %+v", issue.Images)
	}
	f.remove("Saga/cover.jpg")
	f.remove("Saga/Saga 001-poster.jpg")
	f.scan()
	for _, item := range []db.Item{f.titles(db.KindSeries)["Saga"], f.titles(db.KindIssue)["Saga 001"]} {
		if image := shown(item, db.ImageCover); image == nil || image.Width != 30 {
			t.Fatalf("%s first page: %+v", item.Title, item.Images)
		}
	}
	f.s.AllFiles = true
	f.scan()
	for _, item := range []db.Item{f.titles(db.KindSeries)["Saga"], f.titles(db.KindIssue)["Saga 001"]} {
		if candidates, err := f.d.Pictures(ctx, item.PublicID, db.ImageCover); err != nil || len(candidates) != 1 || candidates[0].Source != db.ImageFromEmbedded {
			t.Fatalf("%s duplicated or lost its cover: %+v, %v", item.Title, candidates, err)
		}
	}
	f.s.AllFiles = false
	if stats := f.scan(); stats.RowsWritten != 0 {
		t.Fatalf("unchanged comic scan wrote %d rows", stats.RowsWritten)
	}
}

func TestReadingArchiveCoversRespectPathsAndSizeLimit(t *testing.T) {
	cover := picture(t, 10)
	raw := zipped(t, [2]string{"META-INF/container.xml", `<container><rootfiles><rootfile full-path="OEBPS/book.opf"/></rootfiles></container>`},
		[2]string{"OEBPS/book.opf", `<package><manifest><item href="../../outside.png" properties="cover-image"/></manifest></package>`},
		[2]string{"../outside.png", string(cover)})
	if data, _ := reading.Cover(bytes.NewReader(raw), int64(len(raw)), ".epub", 20<<20); len(data) != 0 {
		t.Fatal("the cover left the archive")
	}
	raw = zipped(t, [2]string{"001.png", string(cover)})
	if data, err := reading.Cover(bytes.NewReader(raw), int64(len(raw)), ".cbz", int64(len(cover)-1)); err == nil || len(data) != 0 {
		t.Fatalf("an oversized cover was read: %d bytes, %v", len(data), err)
	}
}

func TestReviewGenreFolderPictureIsNotAnArtistPortrait(t *testing.T) {
	f := newFixture(t, db.LibraryMusic)
	f.s.Pictures = pictures{}
	f.tagged("Jazz/Kind of Blue/01.mp3", "TIT2", "So What", "TPE1", "Miles Davis", "TALB", "Kind of Blue")
	f.tagged("Jazz/Time Out/01.mp3", "TIT2", "Take Five", "TPE1", "Dave Brubeck", "TALB", "Time Out")
	f.put("Jazz/folder.jpg", picture(t, 11))
	f.scan()
	for _, name := range []string{"Miles Davis", "Dave Brubeck"} {
		if image := shown(f.titles(db.KindArtist)[name], db.ImagePortrait); image != nil {
			t.Errorf("%s got the genre folder's picture as portrait: %+v", name, image)
		}
	}
}

func TestReviewSeriesCoverStaysIssueOneAfterLaterIssueArrives(t *testing.T) {
	f := newFixture(t, db.LibraryComics)
	f.s.Pictures = pictures{}
	f.put("Saga/Saga 001.cbz", zipped(t, [2]string{"1.png", string(picture(t, 31))}))
	f.scan()
	if image := shown(f.titles(db.KindSeries)["Saga"], db.ImageCover); image == nil || image.Width != 31 {
		t.Fatalf("series first cover: %+v", image)
	}
	f.put("Saga/Saga 002.cbz", zipped(t, [2]string{"1.png", string(picture(t, 32))}))
	f.scan()
	series := f.titles(db.KindSeries)["Saga"]
	if image := shown(series, db.ImageCover); image == nil || image.Width != 31 {
		t.Errorf("series cover moved to a later issue: %+v", image)
	}
	candidates, err := f.d.Pictures(ctx, series.PublicID, db.ImageCover)
	if err != nil || len(candidates) != 1 {
		t.Fatalf("later issue added a series cover: %+v, %v", candidates, err)
	}
	f.put("Saga/Saga 003.cbz", zipped(t, [2]string{"1.png", string(picture(t, 33))}))
	f.s.AllFiles = true
	f.scan()
	candidates, err = f.d.Pictures(ctx, series.PublicID, db.ImageCover)
	if err != nil || len(candidates) != 1 || candidates[0].Width != 31 {
		t.Fatalf("Scan all files added later issue covers: %+v, %v", candidates, err)
	}
}
