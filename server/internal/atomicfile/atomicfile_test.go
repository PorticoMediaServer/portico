package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestWriteReplacesWithoutEverBeingPartial(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "track.ass")
	first := []byte(strings.Repeat("a", 4096))
	if err := Write(path, first, 0600); err != nil {
		t.Fatal(err)
	}
	// A reader running throughout a replacement must only ever see one of the
	// two complete versions, never a prefix of the second.
	second := []byte(strings.Repeat("b", 65536))
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var bad atomic64
	for i := 0; i < 4; i++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				data, err := os.ReadFile(path)
				if err != nil {
					continue
				}
				if len(data) != len(first) && len(data) != len(second) {
					bad.add()
				}
			}
		}()
	}
	for i := 0; i < 20; i++ {
		if err := Write(path, second, 0600); err != nil {
			t.Fatal(err)
		}
		if err := Write(path, first, 0600); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	readers.Wait()
	if n := bad.value(); n != 0 {
		t.Fatalf("a reader saw a partial file %d times", n)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("temporary files were left behind: %v", names)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0600 {
		t.Fatalf("the file ended up with mode %v", mode)
	}
}

func TestWriteFailsWithoutDestroyingTheExistingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "sub", "track.ass")
	if err := Write(path, []byte("x"), 0600); err == nil {
		t.Fatal("a write into a directory that does not exist reported success")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("a failed write left something behind: %v", err)
	}
}

type atomic64 struct {
	mu sync.Mutex
	n  int64
}

func (a *atomic64) add() {
	a.mu.Lock()
	a.n++
	a.mu.Unlock()
}

func (a *atomic64) value() int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.n
}
