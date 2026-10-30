package reading

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func sequentialFixture(raw []byte, opens *int) SequentialSource {
	return SequentialSource{Size: int64(len(raw)), Open: func(ctx context.Context) (io.ReadCloser, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		*opens++
		return io.NopCloser(bytes.NewReader(raw)), nil
	}}
}

func TestSequentialPublicationsUseActualFromStartAssetsWithoutReadAt(t *testing.T) {
	ctx := context.Background()
	opens := 0
	e, err := OpenSequentialEPUB(ctx, sequentialFixture(readingtest.EPUB(), &opens))
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Spine) != 1 || e.Spine[0] != "EPUB/chapter.xhtml" || e.Metadata.Title != "Original publication" {
		t.Fatal(e.Metadata, e.Spine)
	}
	b, mime, err := e.Asset(ctx, e.Spine[0])
	if err != nil || mime != "application/xhtml+xml" || !bytes.Contains(b, []byte("source authorization")) {
		t.Fatal(mime, err)
	}
	if opens != 5 {
		t.Fatalf("want one index and four genuine from-start member streams, got%d", opens)
	}
	opens = 0
	c, err := OpenSequentialComic(ctx, sequentialFixture(readingtest.CBZ(), &opens))
	if err != nil {
		t.Fatal(err)
	}
	page, err := c.Page(ctx, 1, 64)
	if err != nil {
		t.Fatal(err)
	}
	if page.Index != 1 || page.Name != "page2.png" || page.Image.Width != 64 || page.Image.Height != 128 || page.Image.MIME != "image/png" {
		t.Fatal(page.Image, page.Name)
	}
	if !bytes.HasPrefix(page.Bytes, []byte{137, 'P', 'N', 'G'}) {
		t.Fatal("page is not an image")
	}
	if opens != 3 {
		t.Fatal("expected index, ComicInfo and selected member only", opens)
	}
}

func TestSequentialArchiveRejectsTraversalCRCTruncationAndChangedBodies(t *testing.T) {
	for _, name := range []string{"../secret.png", "a\\b.png", "/absolute.png"} {
		raw := fixtureZIP(t, [][2]string{{name, "x"}})
		opens := 0
		if _, err := openSequentialArchive(context.Background(), sequentialFixture(raw, &opens)); err == nil {
			t.Fatal(name)
		}
	}
	raw := readingtest.CBZ()
	for i := 0; i < len(raw)-46; i++ {
		if binary.LittleEndian.Uint32(raw[i:]) == 0x02014b50 {
			binary.LittleEndian.PutUint32(raw[i+16:], 7)
			break
		}
	}
	opens := 0
	if _, err := openSequentialArchive(context.Background(), sequentialFixture(raw, &opens)); err == nil {
		t.Fatal("central CRC mismatch accepted")
	}
	raw = readingtest.EPUB()
	opens = 0
	s := sequentialFixture(raw, &opens)
	s.Size++
	if _, err := openSequentialArchive(context.Background(), s); err == nil {
		t.Fatal("wrong source size accepted")
	}
	raw = readingtest.CBZ()
	opens = 0
	s = sequentialFixture(raw, &opens)
	a, err := openSequentialArchive(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	a.sequential.source.Open = func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(bytes.Repeat([]byte{'x'}, len(raw)))), nil
	}
	if _, err = a.read(context.Background(), "page1.png", MaxMember); err == nil {
		t.Fatal("changed representation accepted")
	}
}

func TestSequentialArchiveCancellationAndExpansionBound(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	opens := 0
	if _, err := openSequentialArchive(ctx, sequentialFixture(readingtest.CBZ(), &opens)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	raw := fixtureZIP(t, [][2]string{{"page.png", string(bytes.Repeat([]byte{'x'}, 2<<20))}})
	opens = 0
	if _, err := openSequentialArchive(context.Background(), sequentialFixture(raw, &opens)); !errors.Is(err, ErrLimit) {
		t.Fatal("expansion limit", err)
	}
	if opens != 1 {
		t.Fatal("unsafe index retried/replayed", opens)
	}
}
