//go:build windows

package decoder

import (
	"context"
	"sort"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// displayClassKey is the display-adapter class: every installed graphics driver
// registers one numbered subkey per adapter, with the name and driver version
// Windows itself uses. Reading it needs no elevation and starts no process, so a
// capacity read stays a read. It names adapters; it does not prove a codec. The
// backends listed here are candidates, and each still has to pass a probe before
// anything is claimed about it.
const displayClassKey = `SYSTEM\CurrentControlSet\Control\Class\{4d36e968-e325-11ce-bfc1-08002be10318}`

// pciVendorPrefix is how a display adapter's hardware ID names its vendor.
const pciVendorPrefix = "VEN_"

// windowsVendorBackends maps a PCI vendor to the backends that can drive it on
// Windows. An unknown vendor is listed with no backend rather than a guess.
var windowsVendorBackends = map[string][]HardwareBackend{
	"0x8086": {BackendQSV},
	"0x10de": {BackendNVENC},
	"0x1002": {BackendAMF},
}

// DiscoverHardwareDevices lists this Windows host's adapters: NVIDIA from its
// own inventory, which is probe-addressable by index, and every other vendor
// from the display class Windows maintains.
func DiscoverHardwareDevices(ctx context.Context) []HardwareDevice {
	nvidia := discoverNVIDIADevices(ctx)
	out := append([]HardwareDevice{}, nvidia...)
	for _, device := range discoverWindowsAdapters(ctx) {
		if device.Vendor == "0x10de" && len(nvidia) > 0 {
			// NVML already named this card and can address it, so the registry's
			// copy would be a second row for one GPU.
			continue
		}
		out = append(out, device)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// discoverWindowsAdapters reads the display class's numbered subkeys. A subkey
// without a driver name or hardware id is not an adapter someone installed, and
// is left out instead of being listed as an unknown device.
func discoverWindowsAdapters(ctx context.Context) []HardwareDevice {
	class, err := registry.OpenKey(registry.LOCAL_MACHINE, displayClassKey, registry.READ)
	if err != nil {
		return nil
	}
	defer class.Close()
	names, err := class.ReadSubKeyNames(-1)
	if err != nil {
		return nil
	}
	sort.Strings(names)
	out, seen := []HardwareDevice{}, map[string]bool{}
	for _, name := range names {
		if ctx.Err() != nil {
			break
		}
		// Adapters live in the numbered subkeys; the class key's other children
		// are not devices.
		if len(name) != 4 || strings.Trim(name, "0123456789") != "" {
			continue
		}
		adapter, ok := readWindowsAdapter(ctx, class, name)
		if !ok || seen[adapter.ID] {
			continue
		}
		seen[adapter.ID] = true
		out = append(out, adapter)
	}
	return out
}

func readWindowsAdapter(ctx context.Context, class registry.Key, name string) (HardwareDevice, bool) {
	if ctx.Err() != nil {
		return HardwareDevice{}, false
	}
	key, err := registry.OpenKey(class, name, registry.READ)
	if err != nil {
		return HardwareDevice{}, false
	}
	defer key.Close()
	description := registryString(key, "DriverDesc")
	hardwareID := registryString(key, "MatchingDeviceId")
	if description == "" || hardwareID == "" {
		return HardwareDevice{}, false
	}
	vendor := windowsVendor(hardwareID)
	device := HardwareDevice{
		// The hardware id names this adapter on this machine and does not move
		// between boots, unlike the class key's number.
		ID:            hardwareID,
		Name:          description,
		Vendor:        vendor,
		Driver:        registryString(key, "ProviderName"),
		DriverVersion: registryString(key, "DriverVersion"),
		Backends:      append([]HardwareBackend(nil), windowsVendorBackends[vendor]...),
	}
	return device, true
}

func registryString(key registry.Key, name string) string {
	value, _, err := key.GetStringValue(name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// windowsVendor reads the PCI vendor id out of a hardware id such as
// `PCI\VEN_8086&DEV_4680&SUBSYS_…`. An id without one is left unnamed.
func windowsVendor(hardwareID string) string {
	upper := strings.ToUpper(hardwareID)
	at := strings.Index(upper, pciVendorPrefix)
	if at < 0 || len(upper) < at+len(pciVendorPrefix)+4 {
		return ""
	}
	return "0x" + strings.ToLower(upper[at+len(pciVendorPrefix):at+len(pciVendorPrefix)+4])
}
