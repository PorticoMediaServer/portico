package live

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAViewingWaitsForItsBufferAndThenReadsTheHeldBeginning(t *testing.T) {
	for _, test := range []struct {
		name    string
		bounded bool
	}{{"requested span", false}, {"byte bound", true}} {
		t.Run(test.name, func(t *testing.T) {
			base := time.Unix(2000000000, 0)
			var seconds atomic.Int64
			checked := make(chan struct{}, 1)
			tuning := Tuning{Buffer: 8 * time.Second}
			tuners := &Tuners{now: func() time.Time {
				select {
				case checked <- struct{}{}:
				default:
				}
				return base.Add(time.Duration(seconds.Load()) * time.Second)
			}, tuning: func() Tuning { return tuning }}
			path := filepath.Join(t.TempDir(), "held.ts")
			if err := os.WriteFile(path, []byte("held channel"), 0600); err != nil {
				t.Fatal(err)
			}
			in := &Input{t: tuners, started: base, segments: []segment{{path: path, start: base, size: 12}}}
			in.changed = sync.NewCond(&in.mu)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			output := &firstBytes{want: 12, done: cancel}
			done := make(chan error, 1)
			go func() { done <- (ViewingInput{Input: in}).ReadFrom(ctx, time.Time{}, output, nil) }()
			select {
			case <-checked:
			case <-time.After(time.Second):
				t.Fatal("the viewing did not inspect its buffer")
			}
			in.mu.Lock()
			if output.Len() != 0 {
				in.mu.Unlock()
				t.Fatal("the viewing read before its buffer was ready")
			}
			if test.bounded {
				seconds.Store(4)
				in.segments[0].start = base.Add(time.Second)
			} else {
				seconds.Store(8)
			}
			in.changed.Broadcast()
			in.mu.Unlock()
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("the ready buffer did not start")
			}
			if output.String() != "held channel" {
				t.Fatalf("the held beginning was lost: %q", output.String())
			}
		})
	}
}
