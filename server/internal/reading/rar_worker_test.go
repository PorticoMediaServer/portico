//go:build portico_rar7z

package reading

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/archive7z"
	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

// The worker is the installed server executable's hidden mode; under go test
// that executable is this test binary, so TestMain dispatches the same modes.
func TestMain(m *testing.M) {
	if len(os.Args) >= 2 && os.Args[1] == archive7z.Mode {
		os.Exit(archive7z.Main(os.Args[2:], os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

// stagingReader is the local lease the worker path requires. ReadAtContext and
// Recheck are the parent's deadline and currentness seams.
type stagingReader struct {
	*bytes.Reader
	local bool
}

func (s stagingReader) LocalArchiveStaging() bool { return s.local }

func (s stagingReader) ReadAtContext(ctx context.Context, p []byte, off int64) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.Reader.ReadAt(p, off)
}

func (s stagingReader) Recheck(context.Context) error { return nil }

// openWorkerComic opens an archive the routing sends to the worker.
func openWorkerComic(t *testing.T, raw []byte) *Comic {
	t.Helper()
	comic, err := OpenClassicComic(context.Background(), "cbr", stagingReader{Reader: bytes.NewReader(raw), local: true}, int64(len(raw)))
	if err != nil {
		t.Fatal(err)
	}
	return comic
}

// preflight classifies the three routes from headers alone: stored RAR in
// process, compressed or solid RAR4 and a solid RAR5 file in the worker, and
// encrypted, split, multi-volume or LONG_BLOCK-less headers refused.
func TestPreflightRARRoutes(t *testing.T) {
	for name, test := range map[string]struct {
		raw   []byte
		route rarRoute
		err   bool
	}{
		"stored-rar4":   {readingtest.CBR4(0x30, 0), rarInProcess, false},
		"stored-rar5":   {readingtest.CBR(), rarInProcess, false},
		"compressed":    {readingtest.CBR4Compressed(), rarWorker, false},
		"solid-archive": {readingtest.CBR4Solid(), rarWorker, false},
		"solid-file":    {readingtest.CBR4(0x30, 0x0010), rarWorker, false},
		"encrypted":     {readingtest.CBR4(0x33, 0x0004), 0, true},
		"split":         {readingtest.CBR4(0x30, 0x0002), 0, true},
		"multi-volume":  {setRAR4MainFlag(readingtest.CBR4Compressed(), 0x0001), 0, true},
		"no-long-block": {stripRAR4LongBlock(readingtest.CBR4Compressed()), 0, true},
	} {
		t.Run(name, func(t *testing.T) {
			route, err := preflightRAR(bytes.NewReader(test.raw), int64(len(test.raw)))
			if test.err {
				if !errors.Is(err, ErrArchiveFormat) {
					t.Fatalf("err=%v, want ErrArchiveFormat", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if route != test.route {
				t.Fatalf("route=%v, want %v", route, test.route)
			}
		})
	}
}

// A compressed RAR4 opens through the worker: both pages, with the image facts
// the in-process ZIP path publishes.
func TestOpenClassicComicCompressedRAR4Worker(t *testing.T) {
	raw := readingtest.CBR4Compressed()
	comic := openWorkerComic(t, raw)
	defer comic.members.Close()
	assertFixturePages(t, comic)
	assertWorkerPage(t, raw, "page2.png")
}

// A solid RAR4 opens, and the second page decodes from the window and tables the
// first page built, so its bytes match the same page in a stored archive.
func TestOpenClassicComicSolidRAR4Worker(t *testing.T) {
	raw := readingtest.CBR4Solid()
	comic := openWorkerComic(t, raw)
	defer comic.members.Close()
	assertFixturePages(t, comic)
	assertWorkerPage(t, raw, "page2.png")
}

// Encryption, split volumes and a file header without LONG_BLOCK are refused
// before either decoder runs, through a reader that could stage if asked.
func TestOpenClassicComicRefusesUnsupportedRAR4EvenWhenStageable(t *testing.T) {
	for name, raw := range map[string][]byte{
		"encrypted":     readingtest.CBR4(0x33, 0x0004),
		"multi-volume":  setRAR4MainFlag(readingtest.CBR4Compressed(), 0x0001),
		"no-long-block": stripRAR4LongBlock(readingtest.CBR4Compressed()),
	} {
		_, err := OpenClassicComic(context.Background(), "cbr", stagingReader{Reader: bytes.NewReader(raw), local: true}, int64(len(raw)))
		if !errors.Is(err, ErrArchiveFormat) {
			t.Fatalf("%s: err=%v, want ErrArchiveFormat", name, err)
		}
	}
}

// Stored RAR4 and stored RAR5 stay in process: this lease refuses staging, so a
// worker route would fail instead of opening the comic.
func TestOpenClassicComicStoredRARStaysInProcess(t *testing.T) {
	for name, raw := range map[string][]byte{
		"rar4": readingtest.CBR4(0x30, 0),
		"rar5": readingtest.CBR(),
	} {
		t.Run(name, func(t *testing.T) {
			comic, err := OpenClassicComic(context.Background(), "cbr", stagingReader{Reader: bytes.NewReader(raw), local: false}, int64(len(raw)))
			if err != nil {
				t.Fatal(err)
			}
			defer comic.members.Close()
			assertFixturePages(t, comic)
		})
	}
}

// A compressed archive without a staging lease is the unsupported format, never
// a panic and never an in-process RAR decoder.
func TestOpenClassicComicCompressedRAR4NeedsStaging(t *testing.T) {
	raw := readingtest.CBR4Compressed()
	_, err := OpenClassicComic(context.Background(), "cbr", bytes.NewReader(raw), int64(len(raw)))
	if !errors.Is(err, ErrArchiveFormat) {
		t.Fatalf("err=%v, want ErrArchiveFormat", err)
	}
}

// stalledReader never finishes its extent copy: the deadline it is given is the
// only way out, so the worker path's cancellation is exercised rather than
// raced.
type stalledReader struct{ stagingReader }

func (s stalledReader) ReadAtContext(ctx context.Context, _ []byte, _ int64) (int, error) {
	<-ctx.Done()
	return 0, ctx.Err()
}

// A deadline that expires on the worker path returns that deadline (a bounded
// failure), not a panic and not a half-published archive.
func TestOpenClassicComicCompressedRAR4DeadlineFailsCleanly(t *testing.T) {
	raw := readingtest.CBR4Compressed()
	source := stalledReader{stagingReader{Reader: bytes.NewReader(raw), local: true}}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	comic, err := OpenClassicComic(ctx, "cbr", source, int64(len(raw)))
	if comic != nil {
		t.Fatal("archive published after its deadline")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err=%v, want a deadline", err)
	}
}

func assertFixturePages(t *testing.T, comic *Comic) {
	t.Helper()
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

// assertWorkerPage compares a page decoded through the worker with the same page
// served by the stored RAR4 fixture, so a wrong window or table is a failure,
// not a guess.
func assertWorkerPage(t *testing.T, raw []byte, name string) {
	t.Helper()
	stored := readingtest.CBR4(0x30, 0)
	want := memberBytes(t, bytes.NewReader(stored), int64(len(stored)), name)
	got := memberBytes(t, stagingReader{Reader: bytes.NewReader(raw), local: true}, int64(len(raw)), name)
	if !bytes.Equal(got, want) {
		t.Fatalf("%s decoded %d bytes, want the stored fixture's %d", name, len(got), len(want))
	}
}

func memberBytes(t *testing.T, ra io.ReaderAt, size int64, name string) []byte {
	t.Helper()
	members, err := openMembers(context.Background(), "cbr", ra, size)
	if err != nil {
		t.Fatal(err)
	}
	defer members.Close()
	for _, member := range members.Members() {
		if member.Name != name {
			continue
		}
		body, err := member.Open()
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		out, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	t.Fatalf("%s not among the admitted members", name)
	return nil
}

// setRAR4MainFlag adds a flag to the archive block and fixes its CRC16, as a
// test needs to build a multi-volume header without a RAR writer.
func setRAR4MainFlag(raw []byte, flag uint16) []byte {
	out := append([]byte(nil), raw...)
	if len(out) < 14 || out[7+2] != 0x73 {
		panic("not a RAR4 archive block")
	}
	header := out[7:]
	size := int(binary.LittleEndian.Uint16(header[5:7]))
	binary.LittleEndian.PutUint16(header[3:5], binary.LittleEndian.Uint16(header[3:5])|flag)
	binary.LittleEndian.PutUint16(header[0:2], uint16(crc32.ChecksumIEEE(header[2:size])))
	return out
}
