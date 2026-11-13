package mediaengine

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

func TestConversionFolderReadinessRequiresWritableOutput(t *testing.T) {
	root := t.TempDir()
	e := &Engine{ctx: context.Background(), folder: root, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	p, err := e.open(root, filepath.Join(root, "no-ffmpeg"))
	if err != nil {
		t.Fatal(err)
	}
	e.current = p
	t.Cleanup(func() { p.streams.Shutdown(context.Background()); p.stop() })
	if !e.ConversionFolderReady(root) || e.ConversionFolderReady(filepath.Join(root, "unknown")) {
		t.Fatal("invented readiness")
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := e.open(filepath.Join(root, "file"), ""); err == nil {
		t.Fatal("a non-writable output was ready")
	}
}
