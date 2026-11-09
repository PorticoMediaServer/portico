package hardwarefacts

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFromNamesEachInstallFamily(t *testing.T) {
	cases := []struct {
		name  string
		facts hostFacts
		kind  string
		label string
	}{
		{name: "macOS has no remedy", facts: hostFacts{GOOS: "darwin"}, kind: KindMacOS, label: "macOS"},
		{name: "Windows", facts: hostFacts{GOOS: "windows"}, kind: KindWindows, label: "Windows"},
		{name: "Raspberry Pi 5", facts: hostFacts{GOOS: "linux", Board: "Raspberry Pi 5 Model B Rev 1.0"}, kind: KindRaspberryPi5, label: "Raspberry Pi 5"},
		{name: "Rockchip", facts: hostFacts{GOOS: "linux", Board: "Radxa ROCK 5B (rk3588)"}, kind: KindRockchip, label: "Rockchip RK3588"},
		{name: "Unraid marker", facts: hostFacts{GOOS: "linux", UnraidVersion: true, DockerEnv: true}, kind: KindUnraid, label: "Unraid"},
		{name: "TrueNAS marker", facts: hostFacts{GOOS: "linux", Version: "TrueNAS-SCALE-24.10", DockerEnv: true}, kind: KindTrueNAS, label: "TrueNAS"},
		{name: "Synology marker", facts: hostFacts{GOOS: "linux", SynologyRelease: true, DockerEnv: true}, kind: KindSynology, label: "Synology"},
		{name: "Proxmox LXC only when declared", facts: hostFacts{GOOS: "linux", ContainerRuntime: "lxc", Declared: "proxmox_lxc"}, kind: KindProxmoxLXC, label: "Proxmox LXC"},
		{name: "plain LXC stays neutral", facts: hostFacts{GOOS: "linux", ContainerRuntime: "lxc"}, kind: KindLXC, label: "LXC"},
		{name: "Docker on NVIDIA with the toolkit", facts: hostFacts{GOOS: "linux", DockerEnv: true, NVIDIADevice: true}, kind: KindDockerNVIDIA, label: "Docker, NVIDIA"},
		{name: "Docker on an NVIDIA host without a passed device", facts: hostFacts{GOOS: "linux", DockerEnv: true, PCIVendors: []string{pciNVIDIA}}, kind: KindDockerNVIDIA, label: "Docker, NVIDIA"},
		{name: "Docker on an Intel host", facts: hostFacts{GOOS: "linux", DockerEnv: true, PCIVendors: []string{pciIntel}}, kind: KindDockerIntelAMD, label: "Docker, Intel or AMD"},
		{name: "Docker with no readable vendor", facts: hostFacts{GOOS: "linux", DockerEnv: true}, kind: KindDocker, label: "Docker"},
		{name: "bare Linux", facts: hostFacts{GOOS: "linux"}, kind: KindLinux, label: "Linux"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := From(c.facts)
			if got.Kind != c.kind || got.Label != c.label {
				t.Fatalf("From(%+v) = %q/%q, want %q/%q", c.facts, got.Kind, got.Label, c.kind, c.label)
			}
		})
	}
}

func TestFromUsesDeclaredPlatformAndIgnoresUnknown(t *testing.T) {
	got := From(hostFacts{GOOS: "linux", Declared: "proxmox-lxc", UnraidVersion: true})
	if got.Kind != KindProxmoxLXC {
		t.Fatalf("declared platform = %q, want %q", got.Kind, KindProxmoxLXC)
	}
	got = From(hostFacts{GOOS: "linux", Declared: "not-a-platform", DockerEnv: true})
	if got.Kind != KindDocker {
		t.Fatalf("unknown declaration = %q, want neutral Docker detection to continue", got.Kind)
	}
}

func TestEveryRemedyMatchesTheFrame(t *testing.T) {
	cases := map[string]string{
		KindDockerIntelAMD: RemedyDockerIntelAMD,
		KindDockerNVIDIA:   RemedyDockerNVIDIA,
		KindUnraid:         RemedyUnraid,
		KindTrueNAS:        RemedyTrueNAS,
		KindSynology:       RemedySynology,
		KindProxmoxLXC:     RemedyProxmoxLXC,
		KindWindows:        RemedyWindows,
		KindRaspberryPi5:   RemedyRaspberryPi5,
		KindRockchip:       RemedyRockchip,
	}
	for kind, want := range cases {
		if got := platformFor(kind).Remedy; got != want {
			t.Errorf("%s remedy = %q, want %q", kind, got, want)
		}
	}
	if got := platformFor(KindMacOS); got.Remedy != "" {
		t.Errorf("macOS remedy = %q, want none", got.Remedy)
	}
	if got := platformFor(KindLinux); got.Remedy != "" {
		t.Errorf("Linux remedy = %q, want none", got.Remedy)
	}
	if got := platformFor(KindDocker); got.Remedy != "" {
		t.Errorf("neutral Docker remedy = %q, want none", got.Remedy)
	}
	if got := platformFor(KindLXC); got.Remedy != "" {
		t.Errorf("neutral LXC remedy = %q, want none", got.Remedy)
	}
}

func TestContainerIsObservedNotAssumed(t *testing.T) {
	cases := []struct {
		name      string
		facts     hostFacts
		kind      string
		container bool
	}{
		{name: "board family running in Docker", facts: hostFacts{GOOS: "linux", Board: "Raspberry Pi 5 Model B", DockerEnv: true}, kind: KindRaspberryPi5, container: true},
		{name: "NAS host running natively", facts: hostFacts{GOOS: "linux", Version: "TrueNAS-SCALE-24.10"}, kind: KindTrueNAS, container: false},
		{name: "NAS family in its container", facts: hostFacts{GOOS: "linux", Version: "TrueNAS-SCALE-24.10", DockerEnv: true}, kind: KindTrueNAS, container: true},
		{name: "declared Unraid on a native host", facts: hostFacts{GOOS: "linux", Declared: "unraid"}, kind: KindUnraid, container: false},
		{name: "LXC runtime", facts: hostFacts{GOOS: "linux", ContainerRuntime: "lxc"}, kind: KindLXC, container: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := From(c.facts)
			if got.Kind != c.kind || got.Container != c.container {
				t.Fatalf("From(%+v) = %q container=%v, want %q container=%v", c.facts, got.Kind, got.Container, c.kind, c.container)
			}
		})
	}
}

func TestTargetMatrixMatchesTheFrame(t *testing.T) {
	targets := Targets()
	want := []string{TargetIntel, TargetNVIDIA, TargetAMD, TargetApple, TargetRockchip, TargetRaspberry}
	if len(targets) != len(want) {
		t.Fatalf("targets = %d rows, want %d", len(targets), len(want))
	}
	for index, id := range want {
		if targets[index].ID != id {
			t.Fatalf("target %d = %q, want %q", index, targets[index].ID, id)
		}
	}
	if got := targets[len(targets)-1]; len(got.Encode) != 0 || len(got.Decode) != 1 || got.Decode[0] != "HEVC" {
		t.Fatalf("Raspberry Pi 5 row = %+v, want HEVC decode and no encode", got)
	}
	for _, target := range targets {
		if target.Label == "" {
			t.Errorf("%s has no label", target.ID)
		}
	}
}

func TestTargetForVendor(t *testing.T) {
	cases := map[string]string{"Intel": TargetIntel, "0x10de": TargetNVIDIA, "amd": TargetAMD, "Apple": TargetApple, "RK3588": TargetRockchip, "raspberry-pi-5": TargetRaspberry}
	for vendor, id := range cases {
		target, ok := TargetForVendor(vendor)
		if !ok || target.ID != id {
			t.Errorf("TargetForVendor(%q) = %q/%v, want %q", vendor, target.ID, ok, id)
		}
	}
	if _, ok := TargetForVendor("matrox"); ok {
		t.Error("an unknown vendor matched a target")
	}
	if target, ok := TargetFor(From(hostFacts{GOOS: "linux", Board: "Raspberry Pi 5"})); !ok || target.ID != TargetRaspberry {
		t.Errorf("TargetFor(Raspberry Pi 5) = %q/%v", target.ID, ok)
	}
}

func TestFormatsKeepTheGridOrder(t *testing.T) {
	want := []string{"h264", "hevc8", "hevc10", "av1", "vp9", "vc1", "mpeg2"}
	formats := Formats()
	if len(formats) != len(want) {
		t.Fatalf("formats = %d, want %d", len(formats), len(want))
	}
	for index, id := range want {
		if formats[index].ID != id {
			t.Errorf("format %d = %q, want %q", index, formats[index].ID, id)
		}
	}
}

func TestLaterNamesVulkanVideo(t *testing.T) {
	for _, item := range Later() {
		if item.ID == "vulkan_video" {
			if item.Detail != "Vulkan Video encoding comes after V1." {
				t.Fatalf("Vulkan detail = %q", item.Detail)
			}
			return
		}
	}
	t.Fatal("Vulkan Video encoding is missing from the deferred list")
}

func TestReadPCIVendorsReadsOnlyDisplayControllers(t *testing.T) {
	root := t.TempDir()
	write := func(name, class, vendor string) {
		dir := filepath.Join(root, name)
		if e := os.MkdirAll(dir, 0o755); e != nil {
			t.Fatal(e)
		}
		if e := os.WriteFile(filepath.Join(dir, "class"), []byte(class+"\n"), 0o644); e != nil {
			t.Fatal(e)
		}
		if vendor != "" {
			if e := os.WriteFile(filepath.Join(dir, "vendor"), []byte(vendor+"\n"), 0o644); e != nil {
				t.Fatal(e)
			}
		}
	}
	write("0000:00:02.0", "0x030000", "0x8086")
	write("0000:01:00.0", "0x030200", "0x10DE")
	write("0000:02:00.0", "0x020000", "0x10de") // network controller
	write("0000:03:00.0", "0x030000", "")       // unreadable vendor
	got := readPCIVendors(root)
	if len(got) != 2 || got[0] != pciNVIDIA || got[1] != pciIntel {
		t.Fatalf("readPCIVendors = %v, want [%s %s]", got, pciNVIDIA, pciIntel)
	}
	if readPCIVendors(filepath.Join(root, "missing")) != nil {
		t.Fatal("a missing PCI directory reported vendors")
	}
}
