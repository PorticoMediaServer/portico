package streaming

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// measureCounter is the regression seam for playback-004. It counts the
// directories a sweep actually walks, so a duplicated measurement shows up as
// a number rather than as an inference. Chosen directories can be made to fail,
// which is how the "never reuse a failed count" rule is checked.
type measureCounter struct {
	mu    sync.Mutex
	calls map[string]int
	fail  map[string]error
}

func newMeasureCounter() *measureCounter {
	return &measureCounter{calls: map[string]int{}, fail: map[string]error{}}
}

func (m *measureCounter) count(dir string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[dir]
}

// writeSessionOutput writes a deterministic handful of small files, not the
// hundreds the lock-contention test uses.
func writeSessionOutput(t *testing.T, root, id string, files int) int64 {
	t.Helper()
	directory := filepath.Join(root, id)
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	payload := []byte("segment-bytes")
	for index := 0; index < files; index++ {
		if err := os.WriteFile(filepath.Join(directory, hlsSegmentFile(index)), payload, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return int64(files * len(payload))
}
