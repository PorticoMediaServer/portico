package schematest

import (
	"crypto/sha256"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var errCopyControl = errors.New("copy control")

type errorReader struct{}

func (errorReader) Read([]byte) (int, error) { return 0, errCopyControl }

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errCopyControl }

type shortWriter struct{}

func (shortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) { clear(p); return len(p), nil }

type countedWriter struct{ maximum, total int }

func (w *countedWriter) Write(p []byte) (int, error) {
	if len(p) > w.maximum {
		w.maximum = len(p)
	}
	w.total += len(p)
	return len(p), nil
}

func TestCopyChecksSizeDigestAndIOFailures(t *testing.T) {
	data := "complete immutable copy"
	digest := sha256.Sum256([]byte(data))
	for _, c := range []struct {
		name   string
		reader io.Reader
		writer io.Writer
		size   int64
		digest [sha256.Size]byte
	}{
		{"zero", strings.NewReader(data), io.Discard, 0, digest},
		{"overflow", strings.NewReader(data), io.Discard, math.MaxInt64, digest},
		{"short", strings.NewReader(data[:len(data)-1]), io.Discard, int64(len(data)), digest},
		{"long", strings.NewReader(data + "x"), io.Discard, int64(len(data)), digest},
		{"digest", strings.NewReader(data), io.Discard, int64(len(data)), [sha256.Size]byte{}},
		{"reader", errorReader{}, io.Discard, int64(len(data)), digest},
		{"writer", strings.NewReader(data), errorWriter{}, int64(len(data)), digest},
		{"short_write", strings.NewReader(data), shortWriter{}, int64(len(data)), digest},
	} {
		t.Run(c.name, func(t *testing.T) {
			if err := copyVerified(c.writer, c.reader, c.size, c.digest); err == nil {
				t.Fatal("incomplete copy accepted")
			}
		})
	}
	if err := copyVerified(io.Discard, strings.NewReader(data), int64(len(data)), digest); err != nil {
		t.Fatal(err)
	}
}

func TestCopyBufferIsBounded(t *testing.T) {
	const size = 8 << 20
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(zeroReader{}, size)); err != nil {
		t.Fatal(err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], h.Sum(nil))
	w := &countedWriter{}
	if err := copyVerified(w, io.LimitReader(zeroReader{}, size), size, digest); err != nil {
		t.Fatal(err)
	}
	if w.total != size || w.maximum > 64<<10 {
		t.Fatal("copy is not chunk bounded", w.total, w.maximum)
	}
}

func TestFailedFileCopyRemovesOnlyItsOwnOutput(t *testing.T) {
	dir := t.TempDir()
	source, destination := filepath.Join(dir, "source"), filepath.Join(dir, "copy")
	data := []byte("original")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, destination, int64(len(data)), [sha256.Size]byte{}); err == nil {
		t.Fatal("bad digest accepted")
	}
	if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial output retained", err)
	}
	if err := os.WriteFile(destination, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(source, destination, int64(len(data)), sha256.Sum256(data)); !errors.Is(err, os.ErrExist) {
		t.Fatal("existing output was not refused", err)
	}
	kept, err := os.ReadFile(destination)
	if err != nil || string(kept) != "keep" {
		t.Fatal("original output changed", err)
	}
	if digestFile(t, source) != sha256.Sum256(data) {
		t.Fatal("source changed")
	}
}

func TestAnyTemplateSidecarRefusesPublication(t *testing.T) {
	for _, suffix := range []string{"-wal", "-shm"} {
		for _, content := range []string{"", "uncheckpointed"} {
			path := filepath.Join(t.TempDir(), "canonical.sqlite")
			if err := os.WriteFile(path+suffix, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			if err := noSidecars(path); err == nil {
				t.Fatal("sidecar accepted", suffix, len(content))
			}
		}
	}
}
