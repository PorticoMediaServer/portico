package decoder

import (
	"context"
	"errors"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestHardwareCachedSelectionHonorsOriginalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	d := &HardwareDetector{CachedRuntimeIdentity: func(context.Context, string) (string, bool) {
		calls++
		return "", false
	}}
	for range 2 {
		if _, err := d.SelectCachedContext(ctx, "/original/tool", "auto", ""); !errors.Is(err, context.Canceled) {
			t.Fatal("retired caller was admitted", err)
		}
	}
	if calls != 0 || len(d.warming) != 0 {
		t.Fatal("retired caller started identity work", calls, d.warming)
	}
}

func TestHardwareCachedSelectionCoalescesColdGeneration(t *testing.T) {
	backend := supportedTestBackend(t)
	tool := fakeFFmpeg(t)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	var calls atomic.Int64
	d := &HardwareDetector{
		CachedRuntimeIdentity: func(context.Context, string) (string, bool) { return "", false },
		Run: func(ctx context.Context, _ *exec.Cmd) error {
			if calls.Add(1) == 1 {
				close(entered)
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	}
	key := tool + "/" + string(backend) + "/generation-cold"
	t.Cleanup(func() {
		once.Do(func() { close(release) })
		deadline := time.Now().Add(5 * time.Second)
		for {
			d.mu.Lock()
			warming := d.warming[key]
			d.mu.Unlock()
			if !warming {
				return
			}
			if time.Now().After(deadline) {
				t.Error("background generation did not retire")
				return
			}
			time.Sleep(time.Millisecond)
		}
	})
	selectOne := func() {
		t.Helper()
		probe, err := d.SelectCachedContext(t.Context(), tool, string(backend), "generation-cold")
		if err != nil || probe.Backend != BackendSoftware || !probe.Available || probe.Reason != ProbeWarming {
			t.Fatal("cold selection did not return software immediately", probe, err)
		}
	}
	selectOne()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("background probe did not start")
	}
	for range 32 {
		selectOne()
	}
	if calls.Load() != 1 {
		t.Fatal("cold requests started duplicate probes", calls.Load())
	}
	once.Do(func() { close(release) })
}
