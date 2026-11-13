//go:build linux

package mediaengine

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
	"github.com/PorticoMediaServer/portico/server-next/internal/settings"
)

// Two cards of one backend must remain two choices. Reads use only completed probes;
// a missing or failing chosen card falls through to another, and Processor only stays off.
func TestHardwareReadAndSelectionUseTheSamePhysicalProbes(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	d, err := db.Open(ctx, filepath.Join(t.TempDir(), "main.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	docs, _, err := settings.Open(ctx, d)
	if err != nil {
		t.Fatal(err)
	}
	native, err := exec.LookPath("true")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "ffmpeg")
	if err := os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int64
	var failFirst atomic.Bool
	detector := &decoder.HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		calls.Add(1)
		if failFirst.Load() && strings.Contains(strings.Join(cmd.Args, " "), "first-card") {
			return errors.New("first card unavailable")
		}
		return nil
	}}
	devices := []decoder.HardwareDevice{
		{ID: "gpu-first", Name: "First GPU", Path: "first-card", Backends: []decoder.HardwareBackend{decoder.BackendNVENC}},
		{ID: "gpu-second", Name: "Second GPU", Path: "second-card", Backends: []decoder.HardwareBackend{decoder.BackendNVENC}},
	}
	first := detector.Probe(ctx, path, decoder.BackendNVENC, devices[0].Path)
	if !first.Available {
		t.Fatalf("fixture probe: %+v", first)
	}
	detector.CachedRuntimeIdentity = func(context.Context, string) (string, bool) { return first.Identity, true }
	if p := detector.Probe(ctx, path, decoder.BackendNVENC, devices[1].Path); !p.Available {
		t.Fatalf("second probe: %+v", p)
	}
	ready := make(chan struct{})
	close(ready)
	tools := &toolset{asked: ready, converter: true, ffmpeg: path, hardware: detector, devices: devices}
	e := &Engine{ctx: ctx, settings: docs, asking: tools}
	set := func(choice string) {
		t.Helper()
		raw, _ := json.Marshal(map[string]string{"graphicsDevice": choice})
		if _, err := docs.Write(ctx, 0, "streaming", raw, -1, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	set("gpu-second")
	before := calls.Load()
	state := e.HardwareState(ctx)
	if len(state.Devices) != 2 || !state.Devices[1].InUse || state.Devices[0].InUse || state.LastTest == nil || state.LastError != nil || calls.Load() != before {
		t.Fatalf("physical device read (probe calls %d -> %d): %+v", before, calls.Load(), state)
	}
	if p := e.hardwareCandidates(ctx, tools, false)[0]; p.Device != "second-card" {
		t.Fatalf("planning chose %+v", p)
	}
	set("gpu-removed")
	if p := e.hardwareCandidates(ctx, tools, false)[0]; p.Device != "first-card" {
		t.Fatalf("missing device did not fall through: %+v", p)
	}
	set("gpu-first")
	failFirst.Store(true)
	detector.Reprobe(ctx, path, decoder.BackendNVENC, "first-card")
	if p := e.hardwareCandidates(ctx, tools, false)[0]; p.Device != "second-card" {
		t.Fatalf("failed device did not fall through: %+v", p)
	}
	set("processor")
	if candidates := e.hardwareCandidates(ctx, tools, false); len(candidates) != 1 || candidates[0].Backend != decoder.BackendSoftware {
		t.Fatalf("processor only: %+v", candidates)
	}

	// Test hardware returns while discovery is held; another tap does not start another test.
	entered, release := make(chan struct{}), make(chan struct{})
	var discoveries atomic.Int64
	detector.Devices = func(ctx context.Context) []decoder.HardwareDevice {
		discoveries.Add(1)
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
		}
		return devices[:1] // The previously working second card has been removed.
	}
	e.TestHardware()
	<-entered
	e.TestHardware()
	if !e.HardwareState(ctx).Checking || discoveries.Load() != 1 {
		t.Fatal("hardware test blocked or was duplicated")
	}
	close(release)
	deadline := time.Now().Add(5 * time.Second)
	for e.HardwareState(ctx).Checking {
		if time.Now().After(deadline) {
			t.Fatal("hardware re-test did not finish")
		}
		time.Sleep(time.Millisecond)
	}
	if calls.Load() <= before {
		t.Fatal("Test hardware reused the previous verdicts")
	}
	if p, ok := decoder.LastProbeObservation(decoder.BackendNVENC, "second-card"); !ok || !p.Stopped {
		t.Fatalf("discovery lost a working card without raising its alert: %+v", p)
	}
}
