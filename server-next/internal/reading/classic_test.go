package reading

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestMain(m *testing.M) {
	if handled, err := RunHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// These are original stored RAR5, stored/compressed/solid RAR4, plain-header 7z
// and default 7-Zip (compressed header) publications. Exercise the real limited
// subprocess, not just the libraries.
func TestClassicComicsReadThroughTheBoundedWorker(t *testing.T) {
	for _, name := range []string{"rar5.cbr", "rar4.cbr", "compressed.cbr", "solid.cbr", "comic.cb7", "encoded.cb7"} {
		t.Run(name, func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join("testdata", "classic", name))
			if err != nil {
				t.Fatal(err)
			}
			f := &bookFile{at: bytes.NewReader(raw), size: int64(len(raw))}
			a, err := openBookArchive(context.Background(), f, Format(name))
			if !comicFormat(Format(name)) {
				if !errors.Is(err, ErrFormatLater) {
					t.Fatalf("untagged format: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer a.close()
			comic, err := readComic(a)
			if err != nil {
				t.Fatal(err)
			}
			if len(comic.pages) != 2 || comic.pages[0] != "page1.png" || comic.pages[1] != "page2.png" {
				t.Fatalf("pages: %v", comic.pages)
			}
			if name != "compressed.cbr" && name != "solid.cbr" && comic.direction != "rtl" {
				t.Fatalf("direction: %s", comic.direction)
			}
			page, err := a.entries[comic.pages[1]].Open()
			if err != nil {
				t.Fatal(err)
			}
			defer page.Close()
			body, err := io.ReadAll(page)
			if err != nil || !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
				t.Fatalf("page: %v", err)
			}
			// The scanner reads its details and its cover (the first page) through the same worker.
			info, cover, err := Comic(context.Background(), bytes.NewReader(raw), int64(len(raw)), Format(name), 1<<20)
			if err != nil || !bytes.HasPrefix(cover, []byte("\x89PNG\r\n\x1a\n")) || comic.direction == "rtl" && info == nil {
				t.Fatalf("details for the scanner: %d bytes of ComicInfo, %d of cover, %v", len(info), len(cover), err)
			}
			// Downloaded to read offline, it is a CBZ of the same entries, the same bytes each time.
			var copied, again bytes.Buffer
			if err := comicCopy(context.Background(), f, Format(name), &copied); err != nil {
				t.Fatal(err)
			}
			comicCopy(context.Background(), f, Format(name), &again)
			cbz, err := openArchive(bytes.NewReader(copied.Bytes()), int64(copied.Len()))
			if err != nil || !bytes.Equal(copied.Bytes(), again.Bytes()) {
				t.Fatalf("copy: %v", err)
			}
			if pages, err := readComic(cbz); err != nil || !slices.Equal(pages.pages, comic.pages) || pages.direction != comic.direction {
				t.Fatalf("copy pages %v, %v", pages, err)
			}
			broken := append([]byte(nil), raw...)
			broken[0] = 0
			invalid, err := openBookArchive(context.Background(), &bookFile{at: bytes.NewReader(broken), size: int64(len(broken))}, Format(name))
			if err == nil {
				invalid.close()
				t.Fatal("damaged signature accepted")
			}
		})
	}
}
