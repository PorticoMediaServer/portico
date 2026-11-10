package streaming

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSegmentReadyRejectsAnEmptyFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "segment-000000.ts")
	if segmentReady(path) {
		t.Fatal("a missing segment reported ready")
	}
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if segmentReady(path) {
		t.Fatal("an empty segment reported ready")
	}
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if !segmentReady(path) {
		t.Fatal("a written segment reported not ready")
	}
}
