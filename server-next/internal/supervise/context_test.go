package supervise

import (
	"context"
	"testing"
	"time"
)

func TestLoopBindsActualOperationAndNestedLoopRestoresParent(t *testing.T) {
	ctx := context.Background()
	if Operation(ctx) != "" || Operation(nil) != "" {
		t.Fatal("unsupervised context acquired an operation")
	}
	Loop(ctx, "cmd.metadata", func(parent context.Context) {
		if Operation(parent) != "cmd.metadata" {
			t.Fatal("loop did not bind its operation")
		}
		Loop(parent, "cmd.artwork", func(child context.Context) {
			if Operation(context.WithoutCancel(child)) != "cmd.artwork" {
				t.Fatal("nested loop lost its operation")
			}
		})
		if Operation(parent) != "cmd.metadata" {
			t.Fatal("nested operation overwrote parent")
		}
	})
	if Operation(ctx) != "" {
		t.Fatal("loop mutated caller context")
	}
}

func TestOperationSurvivesSuperviseRestart(t *testing.T) {
	quietLog(t)
	previous := BackoffInitial
	BackoffInitial = time.Millisecond
	t.Cleanup(func() { BackoffInitial = previous })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	operations := make(chan string, 2)
	done := make(chan struct{})
	Go("test.operation", func() {
		defer close(done)
		starts := 0
		Loop(ctx, "cmd.metadata", func(life context.Context) {
			operations <- Operation(life)
			starts++
			if starts == 1 {
				panic("restart")
			}
		})
	})
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("supervised operation did not restart")
	}
	for i := 0; i < 2; i++ {
		if got := <-operations; got != "cmd.metadata" {
			t.Fatalf("operation on attempt %d = %q", i, got)
		}
	}
}
