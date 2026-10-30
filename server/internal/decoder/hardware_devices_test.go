package decoder

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

func TestDeviceSelectorRejectsLegacyPathsAndUnknownIDs(t *testing.T) {
	devices := []HardwareDevice{{ID: "drm-observed-test", Path: "/dev/dri/renderD128"}}
	for _, selection := range []string{"", "vaapi", "/dev/dri/renderD128", "renderD128", "drm-removed"} {
		if _, err := ResolveHardwareDeviceSelection(selection, devices); !errors.Is(err, ErrUnknownHardwareDevice) {
			t.Fatal("legacy or unknown value aliased to an actual device", selection, err)
		}
	}
	for _, selection := range []string{"auto", "software", devices[0].ID} {
		if _, err := ResolveHardwareDeviceSelection(selection, devices); err != nil {
			t.Fatal(selection, err)
		}
	}
}

func TestDeviceSelectionNeverSwitchesAnExplicitFailedAdapter(t *testing.T) {
	backend := supportedTestBackend(t)
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	devices := []HardwareDevice{{ID: "drm-first", Path: "first-device", Backends: []HardwareBackend{backend}}, {ID: "drm-second", Path: "second-device", Backends: []HardwareBackend{backend}}}
	d := HardwareDetector{Devices: func(context.Context) []HardwareDevice { return devices }}
	calls := 0
	d.Run = func(_ context.Context, cmd *exec.Cmd) error {
		calls++
		for _, arg := range confinedArguments(t, cmd) {
			if arg == "first-device" || arg == "vaapi=hw:first-device" || arg == "cuda=hw:first-device" || arg == "vaapi=qsvva:first-device" || arg == "qsv=hw:first-device" {
				return errors.New("controlled first adapter unavailable")
			}
		}
		// VideoToolbox has no device argument; control that backend explicitly.
		if backend == BackendVideoToolbox {
			return errors.New("controlled unavailable adapter")
		}
		return nil
	}
	path := fakeFFmpeg(t)
	probe, err := d.SelectDevice(context.Background(), path, "drm-first")
	if err != nil || probe.Backend != BackendSoftware || probe.SelectedDeviceID != "" || calls != 1 {
		t.Fatal("explicit failure switched to a peer device", probe, err, calls)
	}
	before := calls
	if _, err := d.SelectDevice(context.Background(), path, "/dev/dri/renderD128"); !errors.Is(err, ErrUnknownHardwareDevice) || calls != before {
		t.Fatal("invalid selector reached a probe", err, calls)
	}
}

func TestDeviceSelectionRefusesRemovalDuringProbeAndCancellation(t *testing.T) {
	backend := supportedTestBackend(t)
	clearHardwareCircuit(backend)
	t.Cleanup(func() { clearHardwareCircuit(backend) })
	device := HardwareDevice{ID: "drm-observed", Path: "controlled-device", Backends: []HardwareBackend{backend}}
	present := true
	d := HardwareDetector{Devices: func(context.Context) []HardwareDevice {
		if present {
			return []HardwareDevice{device}
		}
		return nil
	}, Run: func(context.Context, *exec.Cmd) error { present = false; return nil }}
	probe, err := d.SelectDevice(context.Background(), fakeFFmpeg(t), device.ID)
	if !errors.Is(err, ErrUnknownHardwareDevice) || probe.SelectedDeviceID != "" || probe.Backend != BackendSoftware {
		t.Fatal("removed device's probe was published", probe, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.SelectDevice(ctx, "unused", "software"); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled selection published", err)
	}
}

func TestDeviceSelectionExplicitlyRetriesItsObservedDeviceCircuit(t *testing.T) {
	backend := supportedTestBackend(t)
	device := HardwareDevice{ID: "drm-observed-refresh", Path: "controlled-observed-path", Backends: []HardwareBackend{backend}}
	clearDeviceCircuit(backend, device.Path)
	t.Cleanup(func() { clearDeviceCircuit(backend, device.Path) })
	for range 3 {
		RecordHardwareDeviceFailure(backend, device.Path)
	}
	calls := 0
	d := HardwareDetector{Devices: func(context.Context) []HardwareDevice { return []HardwareDevice{device} },
		Run: func(context.Context, *exec.Cmd) error { calls++; return nil }}
	probe, err := d.SelectDevice(context.Background(), fakeFFmpeg(t), device.ID)
	if err != nil || !probe.Available || probe.SelectedDeviceID != device.ID || calls == 0 || deviceCircuitOpen(backend, device.Path) {
		t.Fatal("explicit observed-device operation reused the old failure circuit", probe, err, calls)
	}
}
