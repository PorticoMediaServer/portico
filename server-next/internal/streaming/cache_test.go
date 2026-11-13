package streaming

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEmptyCacheLeavesViewersProducersAndReadersAlone(t *testing.T) {
	root := t.TempDir()
	h := newSweepFixture(root)
	h.streams = map[string]*stream{"playing": {id: "playing"}}
	h.active["encoding"] = func() {}
	h.outputBusy = map[string]int{"reading": 1}
	h.moving = map[string]chan struct{}{"moving": make(chan struct{})}
	old := time.Now().Add(-3 * time.Minute)
	for _, id := range []string{"playing", "encoding", "reading", "moving", ".live-buffer", "orphan", "new-folder"} {
		writeSessionOutput(t, root, id, 1)
		if id != "new-folder" {
			if err := os.Chtimes(filepath.Join(root, id), old, old); err != nil {
				t.Fatal(err)
			}
		}
	}
	s := &Streams{hls: h}
	want := int64(len("segment-bytes"))
	if bytes, err := s.CachedConversions(context.Background(), false); err != nil || bytes != want {
		t.Fatalf("preview: %d %v, want only the abandoned output's %d bytes", bytes, err, want)
	}
	if bytes, err := s.CachedConversions(context.Background(), true); err != nil || bytes != want {
		t.Fatalf("empty: %d %v", bytes, err)
	}
	if _, err := os.Stat(filepath.Join(root, "orphan")); !os.IsNotExist(err) {
		t.Fatalf("the abandoned output stayed: %v", err)
	}
	for _, id := range []string{"playing", "encoding", "reading", "moving", ".live-buffer", "new-folder"} {
		if _, err := os.Stat(filepath.Join(root, id, hlsSegmentFile(0))); err != nil {
			t.Errorf("cache removal touched %s: %v", id, err)
		}
	}
}
