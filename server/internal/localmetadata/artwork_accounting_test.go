package localmetadata

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestLocalArtworkColdAccountingIsPagedAndCancellable: the one-time measurement
// of the artwork directory walks it a page at a time and honours cancellation.
// A refused measurement must not be remembered, so the next call measures the
// exact bytes already on disk.
func TestLocalArtworkColdAccountingIsPagedAndCancellable(t *testing.T) {
	root := t.TempDir()
	service, err := New(root, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	// More than two pages of the 128-entry scan, with varied sizes so the exact
	// total is not an accident of uniform content.
	var want int64
	const files = 320
	for i := 0; i < files; i++ {
		content := bytes.Repeat([]byte{byte('a' + i%26)}, 1+i%13)
		if err = os.WriteFile(filepath.Join(root, fmt.Sprintf("cached-%03d.img", i)), content, 0600); err != nil {
			t.Fatal(err)
		}
		want += int64(len(content))
	}

	var raw bytes.Buffer
	if err = png.Encode(&raw, image.NewRGBA(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = service.store(cancelled, raw.Bytes()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled cold measurement: %v; want context.Canceled", err)
	}
	if used, known := service.ArtworkUsage(); known || used != 0 {
		t.Fatalf("cancelled measurement was accounted: bytes=%d known=%v", used, known)
	}

	used, err := service.MeasuredArtworkUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used != want {
		t.Fatalf("cold measurement = %d; want %d", used, want)
	}

	key, err := service.store(context.Background(), raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if key == "" {
		t.Fatal("valid PNG was not stored")
	}
	used, err = service.MeasuredArtworkUsage(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if used != want+int64(raw.Len()) {
		t.Fatalf("post-store ledger = %d; want %d", used, want+int64(raw.Len()))
	}
	file, mime, err := service.Open(key)
	if err != nil {
		t.Fatal(err)
	}
	file.Close()
	if mime != "image/png" {
		t.Fatalf("stored mime = %q; want image/png", mime)
	}
}
