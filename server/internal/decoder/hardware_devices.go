package decoder

import (
	"context"
	"errors"
	"time"
)

// HardwareDevice is an observed physical adapter, never a codec capability.
// Paths and candidate backend families stay private; a selected device earns
// each codec/depth through HardwareDetector's actual confined probes.
type HardwareDevice struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Vendor            string            `json:"vendor"`
	Driver            string            `json:"driver"`
	DriverVersion     string            `json:"driverVersion,omitempty"`
	PCIAddress        string            `json:"-"`
	DriverDeviceIndex string            `json:"-"`
	Path              string            `json:"-"`
	Backends          []HardwareBackend `json:"-"`
}

var ErrUnknownHardwareDevice = errors.New("unknown hardware device selection")

// ResolveHardwareDeviceSelection accepts the new selector's exact vocabulary.
// Legacy backend names and absolute render paths are deliberately not aliases.
// Existing legacy backend/path APIs keep their meaning until an explicit owner
// save through the new settings facade; this function never migrates them.
func ResolveHardwareDeviceSelection(selection string, devices []HardwareDevice) (*HardwareDevice, error) {
	if selection == "auto" || selection == "software" {
		return nil, nil
	}
	for _, device := range devices {
		if selection != "" && selection == device.ID {
			found := device
			found.Backends = append([]HardwareBackend(nil), device.Backends...)
			return &found, nil
		}
	}
	return nil, ErrUnknownHardwareDevice
}

// SelectDevice is a bounded background/reprobe operation, not a playback read.
// An explicit removed/unknown ID is refused without testing another adapter.
// The caller retains the software route when none of the actual probes passes.
func (d *HardwareDetector) SelectDevice(ctx context.Context, executable, selection string) (HardwareProbe, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return HardwareProbe{}, err
	}
	fallback := HardwareProbe{Backend: BackendSoftware, Available: true, Reason: ProbeDisabled}
	if selection == "software" {
		return fallback, nil
	}
	discover := d.Devices
	if discover == nil {
		discover = DiscoverHardwareDevices
	}
	devices := discover(ctx)
	if err := ctx.Err(); err != nil {
		return HardwareProbe{}, err
	}
	selected, err := ResolveHardwareDeviceSelection(selection, devices)
	if err != nil {
		return fallback, err
	}
	if selected != nil {
		devices = []HardwareDevice{*selected}
	}
	// An explicit discovery/probe operation cannot reuse a positive from before
	// a driver/plugin/device change. Leave the legacy playback cache untouched.
	probeDetector := HardwareDetector{Timeout: d.Timeout, Libraries: d.Libraries, Run: d.Run}
	for _, device := range devices {
		for _, backend := range device.Backends {
			if err := ctx.Err(); err != nil {
				return HardwareProbe{}, err
			}
			probe := probeDetector.Reprobe(ctx, executable, backend, device.Path)
			if err := ctx.Err(); err != nil {
				return HardwareProbe{}, err
			}
			if probe.Available {
				current, err := ResolveHardwareDeviceSelection(device.ID, discover(ctx))
				if err := ctx.Err(); err != nil {
					return HardwareProbe{}, err
				}
				if err != nil || current.Path != device.Path || current.Vendor != device.Vendor || current.Driver != device.Driver || current.DriverVersion != device.DriverVersion {
					return fallback, ErrUnknownHardwareDevice
				}
				probe.SelectedDeviceID = device.ID
				return probe, nil
			}
			fallback.Reason, fallback.Identity = probe.Reason, probe.Identity
		}
	}
	return fallback, nil
}
