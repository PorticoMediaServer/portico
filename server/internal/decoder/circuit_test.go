package decoder

import (
	"context"
	"os/exec"
	"slices"
	"testing"
	"time"
)

func TestHardwareCircuitBlocksBothModesAndReprobesAfterCooldown(t *testing.T) {
	var backend HardwareBackend
	for _, b := range HardwareBackends {
		if backendSpecs[b].supported() {
			backend = b
			break
		}
	}
	if backend == "" {
		t.Skip("no hardware backend")
	}
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	// Count only probes of the backend under test: in auto mode the other
	// backends this platform can host are still probed, as they should be.
	encoder := backendSpecs[backend].encoder
	calls := 0
	d := &HardwareDetector{Run: func(_ context.Context, cmd *exec.Cmd) error {
		if slices.Contains(confinedArguments(t, cmd), encoder) {
			calls++
		}
		return nil
	}}
	path := fakeFFmpeg(t)
	if got := d.Select(context.Background(), path, string(backend), ""); got.Backend != backend {
		t.Fatal(got)
	}
	for range 3 {
		RecordHardwareFailure(backend)
	}
	before := calls
	for _, mode := range []string{string(backend), "auto"} {
		if got := d.Select(context.Background(), path, mode, ""); got.Backend == backend {
			t.Fatalf("%s ignored circuit: %+v", mode, got)
		}
	}
	if calls != before {
		t.Fatal("open circuit ran a probe")
	}
	hardwareRuntimeFailures.Lock()
	for i := range hardwareRuntimeFailures.failures[backend] {
		hardwareRuntimeFailures.failures[backend][i] = time.Now().Add(-11 * time.Minute)
	}
	hardwareRuntimeFailures.Unlock()
	if got := d.Select(context.Background(), path, string(backend), ""); got.Backend != backend {
		t.Fatal(got)
	}
	if calls == before {
		t.Fatal("recovery reused cached pre-failure success")
	}
	if hardwareNeedsProbe(backend) || hardwareCircuitOpen(backend) {
		t.Fatal("successful recovery did not close circuit")
	}
}
