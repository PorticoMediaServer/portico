//go:build linux

package decoder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDRMDiscoveryKeepsPhysicalIDAcrossRenderRenumbering(t *testing.T) {
	root := t.TempDir()
	sysroot, devroot := filepath.Join(root, "sys"), filepath.Join(root, "dev/dri")
	physical := filepath.Join(sysroot, "devices/pci0000:00/0000:00:02.0")
	for _, dir := range []string{physical, devroot, filepath.Join(sysroot, "class/drm/renderD128")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for name, value := range map[string]string{"vendor": "0x8086\n", "device": "0x3e92\n"} {
		if err := os.WriteFile(filepath.Join(physical, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(physical, filepath.Join(sysroot, "class/drm/renderD128/device")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(devroot, "renderD128"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	// Only this filesystem fixture substitutes the character-device predicate.
	observe := func() []HardwareDevice {
		return discoverDRMDevices(context.Background(), sysroot, devroot, func(string) bool { return true })
	}
	first := observe()
	if len(first) != 1 || first[0].Name != "Intel 0x3e92" || len(first[0].Backends) != 2 || !strings.HasPrefix(first[0].ID, "drm-") {
		t.Fatal("observation invented a product model or missed the device", first)
	}
	if err := os.Rename(filepath.Join(devroot, "renderD128"), filepath.Join(devroot, "renderD129")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(sysroot, "class/drm/renderD128"), filepath.Join(sysroot, "class/drm/renderD129")); err != nil {
		t.Fatal(err)
	}
	second := observe()
	if len(second) != 1 || second[0].ID != first[0].ID || second[0].Path == first[0].Path {
		t.Fatal("render renumbering changed physical identity", first, second)
	}
	raw, err := json.Marshal(second[0])
	if err != nil || strings.Contains(string(raw), devroot) || strings.Contains(string(raw), "backends") || strings.Contains(string(raw), "available") {
		t.Fatal("private path/candidates or fabricated availability escaped", string(raw), err)
	}
	if got := discoverDRMDevices(context.Background(), sysroot, devroot, func(string) bool { return false }); len(got) != 0 {
		t.Fatal("ordinary file was treated as a render device", got)
	}
	if err := os.WriteFile(filepath.Join(physical, "vendor"), []byte("malformed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := observe(); len(got) != 0 {
		t.Fatal("malformed device identity admitted", got)
	}
}

func TestDRMDiscoveryRejectsExternalSysfsTargetAndCancellation(t *testing.T) {
	root := t.TempDir()
	sysroot, devroot := filepath.Join(root, "sys"), filepath.Join(root, "dev/dri")
	for _, dir := range []string{devroot, filepath.Join(sysroot, "class/drm/renderD128")} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(devroot, "renderD128"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(sysroot, "class/drm/renderD128/device")); err != nil {
		t.Fatal(err)
	}
	if got := discoverDRMDevices(context.Background(), sysroot, devroot, func(string) bool { return true }); len(got) != 0 {
		t.Fatal("outside sysfs physical tree admitted", got)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := discoverDRMDevices(ctx, sysroot, devroot, func(string) bool { t.Fatal("cancelled discovery inspected devices"); return true }); len(got) != 0 {
		t.Fatal(got)
	}
}
