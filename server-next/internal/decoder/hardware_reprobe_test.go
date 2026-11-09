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

func TestHardwareReprobeRefreshesOnlyRequestedCellAndRetriesCircuit(t *testing.T) {
	backend := supportedTestBackend(t)
	for _, device := range []string{"refresh-device", "peer-device"} {
		clearDeviceCircuit(backend, device)
		t.Cleanup(func() { clearDeviceCircuit(backend, device) })
	}
	fail, calls := false, 0
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error {
		calls++
		if fail {
			return errors.New("controlled current driver refusal")
		}
		return nil
	}}
	path := fakeFFmpeg(t)
	for _, device := range []string{"refresh-device", "peer-device"} {
		if probe := d.Probe(context.Background(), path, backend, device); !probe.Available {
			t.Fatal(probe)
		}
	}
	before := calls
	fail = true
	if probe := d.Probe(context.Background(), path, backend, "refresh-device"); !probe.Available || calls != before {
		t.Fatal("ordinary read did not reuse its completed observation", probe, calls)
	}
	probe := d.Reprobe(context.Background(), path, backend, "refresh-device")
	if probe.Available || probe.Reason != ProbeEncodeFailed || calls != before+1 {
		t.Fatal("explicit refresh reused the old positive", probe, calls)
	}
	if peer := d.SelectCached(path, string(backend), "peer-device"); peer.Backend != backend || !peer.Available || calls != before+1 {
		t.Fatal("refresh changed or reprobed a peer", peer, calls)
	}
	for range 3 {
		RecordHardwareDeviceFailure(backend, "refresh-device")
	}
	if probe := d.Probe(context.Background(), path, backend, "refresh-device"); probe.Reason != ProbeCircuitOpen || calls != before+1 {
		t.Fatal("normal probe bypassed cooldown", probe, calls)
	}
	fail = false
	probe = d.Reprobe(context.Background(), path, backend, "refresh-device")
	if !probe.Available || calls <= before+1 || deviceCircuitOpen(backend, "refresh-device") {
		t.Fatal("explicit measured retry could not recover its own cell", probe, calls)
	}
}

func TestHardwareReprobeRetiresOlderInFlightObservation(t *testing.T) {
	backend := supportedTestBackend(t)
	clearDeviceCircuit(backend, "refresh-race")
	t.Cleanup(func() { clearDeviceCircuit(backend, "refresh-race") })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var first, fail atomic.Bool
	var calls atomic.Int64
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	d := HardwareDetector{Run: func(ctx context.Context, _ *exec.Cmd) error {
		calls.Add(1)
		if !first.Swap(true) {
			close(started)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if fail.Load() {
			return errors.New("controlled newer observation refuses hardware")
		}
		return nil
	}}
	path := fakeFFmpeg(t)
	older := make(chan HardwareProbe, 1)
	go func() { older <- d.Probe(ctx, path, backend, "refresh-race") }()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("old controlled probe never started")
	}
	fail.Store(true)
	current := d.Reprobe(ctx, path, backend, "refresh-race")
	if current.Available || current.Reason != ProbeEncodeFailed {
		t.Fatal("new observation did not run independently", current)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case retired := <-older:
		if retired.Available || retired.Identity != "" || retired.Reason != ProbeWarming {
			t.Fatal("old in-flight result remained publishable", retired)
		}
	case <-ctx.Done():
		t.Fatal("retired probe did not finish")
	}
	before := calls.Load()
	cached := d.Probe(ctx, path, backend, "refresh-race")
	if cached.Available || cached.Identity != current.Identity || cached.Reason != current.Reason || calls.Load() != before {
		t.Fatal("old completion overwrote the new cached result", current, cached, calls.Load())
	}
}

func TestHardwareReprobeCancellationPreservesCompletedCache(t *testing.T) {
	backend := supportedTestBackend(t)
	clearDeviceCircuit(backend, "cancelled-refresh")
	t.Cleanup(func() { clearDeviceCircuit(backend, "cancelled-refresh") })
	calls := 0
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error { calls++; return nil }}
	path := fakeFFmpeg(t)
	if probe := d.Probe(context.Background(), path, backend, "cancelled-refresh"); !probe.Available {
		t.Fatal(probe)
	}
	before := calls
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if probe := d.Reprobe(ctx, path, backend, "cancelled-refresh"); probe.Available || probe.Reason != ProbeTimedOut || calls != before {
		t.Fatal("cancelled refresh ran or published a probe", probe, calls)
	}
	if probe := d.Probe(context.Background(), path, backend, "cancelled-refresh"); !probe.Available || calls != before {
		t.Fatal("already-cancelled request invalidated a completed observation", probe, calls)
	}
}
