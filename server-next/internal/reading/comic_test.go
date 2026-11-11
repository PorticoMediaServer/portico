package reading

import (
	"context"
	"errors"
	"testing"
	"time"
)

// When the memory for making pages smaller stays in use, a page asked for waits a short while
// and is then told the server is busy; it never waits for as long as another reader holds on.
func TestAPageWaitsForMemoryOnlyAShortWhile(t *testing.T) {
	busyWait = 50 * time.Millisecond
	defer func() { busyWait = 10 * time.Second }()
	b := &budget{freed: make(chan struct{})}
	if err := b.take(context.Background(), memoryLimit); err != nil {
		t.Fatal(err)
	}
	if err := b.take(context.Background(), 1); !errors.Is(err, errBusy) {
		t.Fatalf("a page asked for while the memory was all held: %v", err)
	}
	b.give(memoryLimit)
	if err := b.take(context.Background(), memoryLimit); err != nil {
		t.Fatalf("once the memory was given back: %v", err)
	}
	if err := b.take(context.Background(), memoryLimit+1); !errors.Is(err, errPageTooLarge) {
		t.Fatalf("a page that could never fit: %v", err)
	}
}
