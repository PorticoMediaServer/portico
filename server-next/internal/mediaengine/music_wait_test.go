package mediaengine

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// A cold track must not silently lose its exact trim when measurement takes
// longer than the old three-second planning window.
func TestMusicPreparationWaitsForExactFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	m := &measuredMusic{done: make(chan struct{})}
	e := Engine{music: map[string]*measuredMusic{"track\x000\x000": m}}
	result := make(chan bool, 1)
	go func() { got, ok := e.measured(ctx, 1, "track", db.FileOnDisk{}); result <- ok && got == m }()
	select {
	case <-result:
		t.Fatal("preparation finished before the measurement")
	case <-time.After(3200 * time.Millisecond):
	}
	close(m.done)
	select {
	case ok := <-result:
		if !ok {
			t.Fatal("measured plan was lost")
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
