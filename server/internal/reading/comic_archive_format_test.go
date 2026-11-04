package reading

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/readingtest"
)

func TestComicArchiveFormatMismatch(t *testing.T) {
	for _, tc := range []struct {
		name, format string
		raw          []byte
	}{
		{"RAR-as-CBZ", "cbz", readingtest.CBR()},
		{"7z-as-CBZ", "cbz", readingtest.CB7()},
		{"ZIP-as-CBR", "cbr", readingtest.CBZ()},
		{"7z-as-CBR", "cbr", readingtest.CB7()},
		{"ZIP-as-CB7", "cb7", readingtest.CBZ()},
		{"RAR-as-CB7", "cb7", readingtest.CBR()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			if tc.format == "cbz" {
				_, err = OpenComic(context.Background(), bytes.NewReader(tc.raw), int64(len(tc.raw)))
			} else {
				_, err = OpenClassicComic(context.Background(), tc.format, bytes.NewReader(tc.raw), int64(len(tc.raw)))
			}
			if !errors.Is(err, ErrArchiveFormat) {
				t.Fatalf("mislabeled %s = %v, want ErrArchiveFormat", tc.format, err)
			}
			if tc.format == "cbz" {
				opens := 0
				if _, err := OpenSequentialComic(context.Background(), sequentialFixture(tc.raw, &opens)); !errors.Is(err, ErrArchiveFormat) {
					t.Fatalf("sequential mislabeled CBZ = %v, want ErrArchiveFormat", err)
				}
				if opens != 1 {
					t.Fatalf("sniffing reopened the source %d times", opens)
				}
			}
		})
	}
}

func TestComicArchiveFormatKeepsMalformedAndSafetyErrors(t *testing.T) {
	for _, raw := range [][]byte{bytes.Repeat([]byte("x"), 32), append([]byte("PK\x03\x04"), make([]byte, 28)...)} {
		if _, err := OpenComic(context.Background(), bytes.NewReader(raw), int64(len(raw))); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed ZIP = %v, want ErrInvalid", err)
		}
		opens := 0
		if _, err := OpenSequentialComic(context.Background(), sequentialFixture(raw, &opens)); !errors.Is(err, ErrInvalid) {
			t.Fatalf("malformed sequential ZIP = %v, want ErrInvalid", err)
		}
	}
	raw := readingtest.CBR()
	if _, err := OpenComic(context.Background(), bytes.NewReader(raw), MaxSource+1); !errors.Is(err, ErrLimit) {
		t.Fatalf("oversized source = %v, want ErrLimit", err)
	}
	r := &countReader{ReaderAt: bytes.NewReader(raw)}
	if _, err := OpenComic(context.Background(), r, int64(len(raw))); !errors.Is(err, ErrArchiveFormat) || r.max != 8 {
		t.Fatalf("mismatch read extent = %d, error = %v", r.max, err)
	}
}

func TestComicArchiveFormatKeepsSourceFailures(t *testing.T) {
	raw := readingtest.CBZ()
	failure := errors.New("source unavailable during signature read")
	r := &interruptedArchiveReader{ReaderAt: bytes.NewReader(raw), err: failure}
	if _, err := OpenComic(context.Background(), r, int64(len(raw))); !errors.Is(err, failure) {
		t.Fatalf("signature source failure = %v", err)
	}
	source := SequentialSource{Size: int64(len(raw)), Open: func(context.Context) (io.ReadCloser, error) {
		return io.NopCloser(io.NewSectionReader(r, 0, int64(len(raw)))), nil
	}}
	if _, err := OpenSequentialComic(context.Background(), source); !errors.Is(err, failure) {
		t.Fatalf("sequential signature source failure = %v", err)
	}
}
