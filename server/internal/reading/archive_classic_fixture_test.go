//go:build portico_rar7z

package reading

import (
	"bytes"
	"context"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestOpenClassicComicReadsFixtureCBR(t *testing.T) {
	raw := readingtest.CBR()
	comic, err := OpenClassicComic(context.Background(), "cbr", bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer comic.members.Close()
	if len(comic.Names) != 2 || comic.Names[0] != "page1.png" || comic.Names[1] != "page2.png" {
		t.Fatalf("pages=%v", comic.Names)
	}
	for index := range comic.Names {
		page, err := comic.Page(context.Background(), index, 0)
		if err != nil {
			t.Fatal(err)
		}
		if page.Image.MIME != "image/png" || page.Image.Width != 128 || page.Image.Height != 256 {
			t.Fatalf("page %d image=%+v", index, page.Image)
		}
	}
}

// A CBR's root ComicInfo.xml reaches the reader as metadata, as CB7's does; it is
// never listed as a page.
func TestOpenClassicComicReadsCBRComicInfo(t *testing.T) {
	raw := readingtest.CBR()
	comic, err := OpenClassicComic(context.Background(), "cbr", bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer comic.members.Close()
	if comic.Metadata.Title != "Original comic" || comic.Metadata.Author != "Portico" || comic.Metadata.Language != "en" || comic.Metadata.Direction != "rtl" {
		t.Fatalf("ComicInfo facts: %+v", comic.Metadata)
	}
	for _, name := range comic.Names {
		if name == "ComicInfo.xml" {
			t.Fatal("ComicInfo.xml listed as a page")
		}
	}
	if !rarComicMember("ComicInfo.xml", 100) || rarComicMember("ComicInfo.xml", 0) || rarComicMember("sub/ComicInfo.xml", 100) || rarComicMember("comicinfo.xml", 100) || rarComicMember("notes.txt", 100) {
		t.Fatal("only the exact root ComicInfo.xml, bounded, besides pages")
	}
}
