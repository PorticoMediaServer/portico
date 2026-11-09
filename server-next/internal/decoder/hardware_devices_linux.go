//go:build linux

package decoder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var renderNodeName = regexp.MustCompile(`^renderD[0-9]+$`)
var pciDeviceID = regexp.MustCompile(`^0x[0-9a-f]{4}$`)

func readDRMAttribute(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 257))
	if err != nil || len(raw) > 256 {
		return ""
	}
	value := strings.TrimSpace(string(raw))
	for _, r := range value {
		if r < 32 || r == 127 {
			return ""
		}
	}
	return value
}

// DiscoverHardwareDevices currently observes Linux DRM adapters. Other native
// inventories are added only with their own measured producer; no fabricated
// "default GPU" is returned for a platform without an implemented inventory.
func DiscoverHardwareDevices(ctx context.Context) []HardwareDevice {
	drm := discoverDRMDevices(ctx, "/sys", "/dev/dri", func(path string) bool {
		info, err := os.Stat(path)
		return err == nil && info.Mode()&os.ModeDevice != 0 && info.Mode()&os.ModeCharDevice != 0
	})
	nvidia := discoverNVIDIADevices(ctx)
	matched := map[string]bool{}
	for _, device := range nvidia {
		matched[device.PCIAddress] = true
	}
	out := append([]HardwareDevice{}, nvidia...)
	for _, device := range drm {
		if device.Vendor != "0x10de" || !matched[device.PCIAddress] {
			out = append(out, device)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func discoverDRMDevices(ctx context.Context, sysroot, devroot string, renderDevice func(string) bool) []HardwareDevice {
	nodes, _ := filepath.Glob(filepath.Join(devroot, "renderD*"))
	out, seen := []HardwareDevice{}, map[string]bool{}
	for i, node := range nodes {
		if i >= 64 || ctx.Err() != nil {
			break
		}
		if !renderNodeName.MatchString(filepath.Base(node)) || !renderDevice(node) {
			continue
		}
		device, err := filepath.EvalSymlinks(filepath.Join(sysroot, "class/drm", filepath.Base(node), "device"))
		if err != nil {
			continue
		}
		relative, err := filepath.Rel(filepath.Join(sysroot, "devices"), device)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		vendor, pci := strings.ToLower(readDRMAttribute(filepath.Join(device, "vendor"))), strings.ToLower(readDRMAttribute(filepath.Join(device, "device")))
		if !pciDeviceID.MatchString(vendor) || !pciDeviceID.MatchString(pci) {
			continue
		}
		// The sysfs physical identity survives render-node renumbering. A new
		// device at a different physical path gets a different opaque selector.
		sum := sha256.Sum256([]byte(filepath.ToSlash(relative) + "\x00" + vendor + "\x00" + pci))
		id := "drm-" + hex.EncodeToString(sum[:16])
		if seen[id] {
			continue
		}
		seen[id] = true
		name := readDRMAttribute(filepath.Join(device, "label"))
		if name == "" {
			brand := map[string]string{"0x8086": "Intel", "0x1002": "AMD", "0x10de": "NVIDIA"}[vendor]
			if brand == "" {
				brand = "Graphics device"
			}
			name = brand + " " + pci
		}
		driver, _ := filepath.EvalSymlinks(filepath.Join(device, "driver"))
		if driver != "" {
			driver = filepath.Base(driver)
		}
		candidates := []HardwareBackend{}
		if vendor == "0x8086" {
			candidates = []HardwareBackend{BackendQSV, BackendVAAPI}
		} else if vendor == "0x1002" {
			candidates = []HardwareBackend{BackendVAAPI}
		}
		// NVIDIA CUDA selectors require its own observed driver UUID/index
		// association. A DRM node alone must never invent CUDA device zero.
		out = append(out, HardwareDevice{ID: id, Name: name, Vendor: vendor, Driver: driver, PCIAddress: normalizedPCIAddress(filepath.Base(device)), Path: node, Backends: candidates})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
