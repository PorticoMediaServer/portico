package decoder

import (
	"context"
	"os/exec"
	"testing"
)

func TestHardwareDeviceCircuitNeverDisablesPeerOrDefault(t *testing.T) {
	backend := supportedTestBackend(t)
	for _, device := range []string{"", "controlled-first", "controlled-peer"} {
		clearDeviceCircuit(backend, device)
		defer clearDeviceCircuit(backend, device)
	}
	for range 3 {
		RecordHardwareDeviceFailure(backend, "controlled-first")
	}
	if !deviceCircuitOpen(backend, "controlled-first") || deviceCircuitOpen(backend, "controlled-peer") || hardwareCircuitOpen(backend) {
		t.Fatal("device failure poisoned a peer or the legacy default")
	}
	d := HardwareDetector{Run: func(context.Context, *exec.Cmd) error { return nil }}
	path := fakeFFmpeg(t)
	failed := d.Probe(context.Background(), path, backend, "controlled-first")
	if failed.Available || failed.Reason != ProbeCircuitOpen {
		t.Fatal("open device circuit was ignored", failed)
	}
	peer := d.Probe(context.Background(), path, backend, "controlled-peer")
	if !peer.Available || !deviceCircuitOpen(backend, "controlled-first") {
		t.Fatal("peer recovery erased the failed device or could not run", peer)
	}
	selected := d.SelectCached(path, string(backend), "controlled-peer")
	if !selected.Available || selected.Backend != backend {
		t.Fatal("cached peer was disabled by another device", selected)
	}
	if !deviceNeedsProbe(backend, "controlled-first") || deviceNeedsProbe(backend, "controlled-peer") {
		t.Fatal("recovery qualification crossed device boundary")
	}
}
