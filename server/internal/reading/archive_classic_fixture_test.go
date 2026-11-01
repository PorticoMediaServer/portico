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
