//go:build portico_rar7z

package reading

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
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

// OPS-2: a legacy RAR4 CBR whose pages are stored opens and reads like a RAR5 one;
// no RAR4 decoder runs for a stored member.
func TestOpenClassicComicReadsStoredRAR4(t *testing.T) {
	raw := readingtest.CBR4(0x30, 0)
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

// Encrypted, split and LONG_BLOCK-less RAR4 members are refused from their
// headers as an unsupported format (a definite 422 to clients), never decoded
// and never a 500. Compressed and solid RAR4 are routed to the worker instead;
// a plain reader cannot stage them, so they stay unsupported here too.
func TestOpenClassicComicRefusesUnsupportedRAR4(t *testing.T) {
	for name, raw := range map[string][]byte{
		"compressed":    readingtest.CBR4(0x33, 0),
		"solid":         readingtest.CBR4(0x30, 0x0010),
		"encrypted":     readingtest.CBR4(0x30, 0x0004),
		"split":         readingtest.CBR4(0x30, 0x0002),
		"no-long-block": stripRAR4LongBlock(readingtest.CBR4(0x30, 0)),
	} {
		_, err := OpenClassicComic(context.Background(), "cbr", bytes.NewReader(raw), int64(len(raw)))
		if !errors.Is(err, ErrArchiveFormat) {
			t.Fatalf("%s: err=%v, want ErrArchiveFormat", name, err)
		}
	}
}

// stripRAR4LongBlock clears LONG_BLOCK on every file header and fixes its CRC16, as
// the non-standard writer of the OPS-2 walk fixture did.
func stripRAR4LongBlock(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	for at := 7; at+7 <= len(out); {
		kind, size := out[at+2], int(binary.LittleEndian.Uint16(out[at+5:at+7]))
		next := at + size
		if kind == 0x74 {
			packed := int(binary.LittleEndian.Uint32(out[at+7 : at+11]))
			flags := binary.LittleEndian.Uint16(out[at+3:at+5]) &^ 0x8000
			binary.LittleEndian.PutUint16(out[at+3:at+5], flags)
			binary.LittleEndian.PutUint16(out[at:at+2], uint16(crc32.ChecksumIEEE(out[at+2:next])))
			next += packed
		}
		if kind == 0x7b {
			break
		}
		at = next
	}
	return out
}
