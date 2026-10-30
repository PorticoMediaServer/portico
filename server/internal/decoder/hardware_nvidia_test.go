package decoder

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const controlledNVIDIAInventory = "GPU-11111111-2222-3333-4444-555555555555, 00000000:01:00.0, 2, Controlled NVIDIA adapter, 570.12.3\n"

func TestNVIDIAInventoryKeepsUUIDIdentityAcrossIndexChanges(t *testing.T) {
	first := parseNVIDIADiscovery(controlledNVIDIAInventory)
	second := parseNVIDIADiscovery(strings.Replace(controlledNVIDIAInventory, ", 2,", ", 4,", 1))
	if len(first) != 1 || len(second) != 1 || first[0].ID != second[0].ID || first[0].DriverDeviceIndex != "2" || second[0].DriverDeviceIndex != "4" || first[0].PCIAddress != "0:01:00.0" || first[0].DriverVersion != "570.12.3" {
		t.Fatal("observed UUID did not bind the index", first, second)
	}
	if len(first[0].Backends) != 0 || first[0].Path != "" {
		t.Fatal("NVML index incorrectly became a selected CUDA ordinal", first)
	}
	raw, err := json.Marshal(first[0])
	if err != nil || strings.Contains(string(raw), "GPU-111") || strings.Contains(string(raw), "available") || strings.Contains(string(raw), "sessionLimit") || strings.Contains(string(raw), "backends") || strings.Contains(string(raw), "pciAddress") {
		t.Fatal("reporter invented codec/session facts or leaked private selector details", string(raw), err)
	}
}

func TestNVIDIAReporterKeepsFixedArgumentsAndPrivateEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("controlled POSIX reporter fixture")
	}
	t.Setenv("PORTICO_TEST_GPU_SECRET", "controlled-private-value")
	path := filepath.Join(t.TempDir(), "nvidia-smi")
	script := "#!/bin/sh\nset -eu\n" +
		"test \"$#\" = 2\ntest \"$1\" = '--query-gpu=uuid,pci.bus_id,index,name,driver_version'\n" +
		"test \"$2\" = '--format=csv,noheader,nounits'\ntest -z \"${PORTICO_TEST_GPU_SECRET:-}\"\n" +
		"printf '%s' '" + controlledNVIDIAInventory + "'\n"
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := runNVIDIADiscovery(context.Background(), path)
	if err != nil || raw != controlledNVIDIAInventory {
		t.Fatal("fixed reporter contract or environment fence failed", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexec sleep 10\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := runNVIDIADiscovery(ctx, path); err == nil || time.Since(started) > 2*time.Second {
		t.Fatal("reporter did not honor caller cancellation", err, time.Since(started))
	}
}

func TestNVIDIAInventoryRefusesMalformedAndAmbiguousSnapshots(t *testing.T) {
	for _, raw := range []string{
		strings.Replace(controlledNVIDIAInventory, "GPU-11111111-2222-3333-4444-555555555555", "guessed-gpu", 1),
		strings.Replace(controlledNVIDIAInventory, "00000000:01:00.0", "unknown", 1),
		strings.Replace(controlledNVIDIAInventory, ", 2,", ", 999,", 1),
		strings.Replace(controlledNVIDIAInventory, "570.12.3", "Not Supported", 1),
		controlledNVIDIAInventory + controlledNVIDIAInventory,
		controlledNVIDIAInventory + strings.Replace(controlledNVIDIAInventory, "GPU-11111111", "GPU-aaaaaaaa", 1),
		"unexpected,field,count\n",
		strings.Repeat("x", (1<<20)+1),
	} {
		if got := parseNVIDIADiscovery(raw); len(got) != 0 {
			t.Fatal("ambiguous/malformed system report became a device", got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := discoverNVIDIADevices(ctx); len(got) != 0 {
		t.Fatal("cancelled discovery produced devices", got)
	}
}
