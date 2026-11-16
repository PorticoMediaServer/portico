package scan_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

type stuckOn struct {
	name    string
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func (p *stuckOn) Probe(ctx context.Context, path string) (scan.Facts, error) {
	if filepath.Base(path) == p.name {
		p.once.Do(func() { close(p.reached) })
		<-p.release
	}
	return scan.Facts{Container: "matroska", DurationMS: 60_000}, nil
}

// A scan held on one file while reading names that file, though the others of its batch are
// read around it.
func TestAScanHeldOnAFileNamesThatFile(t *testing.T) {
	f := newFixture(t, db.LibraryMovies)
	for n := 1; n <= 12; n++ {
		f.write(fmt.Sprintf("Film %02d (2001).mkv", n))
	}
	stuck := "Film 09 (2001).mkv"
	p := &stuckOn{name: stuck, reached: make(chan struct{}), release: make(chan struct{})}
	f.s.Prober = p
	var mu sync.Mutex
	var current string
	named := make(chan struct{}, 1)
	f.s.Progress = func(stats scan.Stats) {
		mu.Lock()
		current = stats.Current
		if current == stuck {
			select {
			case named <- struct{}{}:
			default:
			}
		}
		mu.Unlock()
	}
	done := make(chan struct{})
	go func() { defer close(done); f.scan() }()
	release := sync.OnceFunc(func() { close(p.release); <-done })
	defer release()
	select {
	case <-p.reached:
	case <-time.After(20 * time.Second):
		t.Fatal("the scan never reached the file")
	}
	select {
	case <-named:
	case <-time.After(30 * time.Second):
		t.Fatal("the scan did not name the held file")
	}
	mu.Lock()
	got := current
	mu.Unlock()
	release()
	if got != stuck {
		t.Fatalf("the scan is held on %q and its progress names %q", stuck, got)
	}
}
