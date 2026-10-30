package imagework

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestDecoderGateBoundsWaitingCancelsAndRecovers(t *testing.T) {
	g := &gate{active: make(chan struct{}, 1), waiting: make(chan struct{}, 8)}
	release, err := g.acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		go func() {
			done, e := g.acquire(ctx)
			if done != nil {
				done()
			}
			results <- e
		}()
	}
	until := time.Now().Add(time.Second)
	for len(g.waiting) != 8 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if len(g.waiting) != 8 {
		t.Fatalf("waiting bound setup failed: %d", len(g.waiting))
	}
	if done, e := g.acquire(context.Background()); !errors.Is(e, ErrBusy) {
		if done != nil {
			done()
		}
		t.Fatalf("overflow not refused: %v", e)
	}
	cancel()
	for i := 0; i < 8; i++ {
		select {
		case e := <-results:
			if !errors.Is(e, context.Canceled) {
				t.Fatalf("waiting work escaped cancellation: %v", e)
			}
		case <-time.After(time.Second):
			t.Fatal("cancelled decoder work remained queued")
		}
	}
	if len(g.waiting) != 0 {
		t.Fatalf("waiting slots leaked: %d", len(g.waiting))
	}
	release()
	release()
	next, e := g.acquire(context.Background())
	if e != nil {
		t.Fatalf("gate failed to recover: %v", e)
	}
	next()
	if len(g.active) != 0 {
		t.Fatal("decoder slot leaked")
	}
}
