package decoder

import "context"

// Known is what has been found out about each hardware backend this build can use on this
// system, for the owner's "What this server can do", in the order playback tries them. It
// reads only the verdicts already kept for this executable: it never runs a probe, so a
// backend not tried yet (the first that works is used, and those after it are not tried) is
// absent, and before anything is tried the list is empty. A backend whose device stopped
// working since it was tried is answered as such, with ProbeCircuitOpen.
func (d *HardwareDetector) Known(ctx context.Context, executable string) []HardwareProbe {
	return d.KnownDevice(ctx, executable, "")
}

// KnownDevice reads the same completed codec cells as playback for one observed selector.
// It never starts a probe; a missing cell stays unknown.
func (d *HardwareDetector) KnownDevice(ctx context.Context, executable, device string) []HardwareProbe {
	lookup := d.CachedRuntimeIdentity
	if lookup == nil {
		lookup = cachedHardwareIdentity
	}
	identity, ready := lookup(ctx, executable)
	if !ready {
		return nil
	}
	var out []HardwareProbe
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, backend := range HardwareBackends {
		if spec, ok := backendSpecs[backend]; !ok || !spec.supported() {
			continue
		}
		probe, ok := d.cache[identity+"/"+string(backend)+"/"+device]
		if !ok {
			continue
		}
		if probe.Available && deviceCircuitOpen(backend, device) {
			probe.Available, probe.Reason = false, ProbeCircuitOpen
		} else if probe.Available && deviceNeedsProbe(backend, device) {
			probe.Available, probe.Reason = false, ProbeWarming
		}
		out = append(out, probe)
	}
	return out
}

// InstalledToolchain is what the FFmpeg in force was found to do at its trial, whatever the
// owner's device setting is now (CurrentToolchain hides the graphics filters under software
// encoding): what "What this server can do" says the machine has. nil before the trial.
func InstalledToolchain() *ToolchainFacts { return installedToolchain.Load() }
