// Package hardwarefacts names the install this server runs on and the V1
// hardware target matrix. The platform supplies the one plain remedy the owner
// sees when no GPU is usable (HARDWARE HW-7); the matrix is the target each
// vendor is measured against in Settings.
//
// Neither is a claim about this host. The decoder's probes decide what this
// server can actually do; a target says what the install is meant to reach.
package hardwarefacts

import (
	"os"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Platform is the install family this server runs on. Kind is a stable string
// published on diagnostics; Label is the name the frame's remedy table uses.
type Platform struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	// Container is what the process actually observes (a container marker or
	// runtime), never an assumption from the install family: a board image can
	// run in Docker, and a NAS host can run the server directly.
	Container bool `json:"container"`
	// Remedy is the one plain thing the owner of this platform can do when no
	// GPU is usable. Empty means nothing is wrong with this install's GPU
	// access, the platform has no single remedy to offer, or the host's
	// graphics vendor is not known well enough to name one.
	Remedy string `json:"remedy,omitempty"`
}

// Platform kinds. These are published, so they are stable strings rather than
// free text.
const (
	KindDockerIntelAMD = "docker_intel_amd"
	KindDockerNVIDIA   = "docker_nvidia"
	// KindDocker is a neutral container: the process is containerized but the
	// host's graphics vendor could not be established, so no vendor remedy is
	// claimed.
	KindDocker   = "docker"
	KindUnraid   = "unraid"
	KindTrueNAS  = "truenas"
	KindSynology = "synology"
	// KindProxmoxLXC is only published when the install declares Proxmox or a
	// Proxmox marker is read. An LXC runtime alone is not Proxmox evidence.
	KindProxmoxLXC = "proxmox_lxc"
	// KindLXC is a neutral LXC container whose host platform is unknown.
	KindLXC          = "lxc"
	KindWindows      = "windows"
	KindMacOS        = "macos"
	KindRaspberryPi5 = "raspberry_pi_5"
	KindRockchip     = "rockchip"
	KindLinux        = "linux"
)

// PlatformEnv names the install's platform explicitly. Unraid, TrueNAS,
// Synology and Proxmox run Portico in a container, where the host's own markers
// are not visible from inside; their templates set this so the owner sees their
// platform's remedy rather than the container's.
const PlatformEnv = "PORTICO_PLATFORM"

// PCI vendor identifiers: the host's graphics vendor, which a container can
// read from /sys/bus/pci even when no GPU device was passed in.
const (
	pciIntel   = "0x8086"
	pciNVIDIA  = "0x10de"
	pciAMD     = "0x1002"
	pciDisplay = "0x03" // class 0x03: display controller (0x0300, 0x0302, …)
)

// hostFacts is what platform detection reads. Tests supply it directly; the
// live path fills it from the filesystem and environment.
type hostFacts struct {
	GOOS string
	// Declared is the explicit PORTICO_PLATFORM value, if the install set one.
	Declared string
	// Board is /proc/device-tree/model on single-board computers.
	Board string
	// Version is /etc/version (TrueNAS) and Release is /etc/os-release.
	Version string
	Release string
	// Markers are the files that identify an install or a container runtime.
	UnraidVersion    bool
	SynologyRelease  bool
	DockerEnv        bool
	ContainerEnv     bool
	ContainerRuntime string
	NVIDIADevice     bool
	// PCIVendors are the vendor IDs of the host's display controllers, ordered
	// so detection is repeatable.
	PCIVendors []string
}

// Remedies are the frame's one line per platform, word for word.
const (
	RemedyDockerIntelAMD = "Give Portico your graphics card: add the device /dev/dri to this container."
	RemedyDockerNVIDIA   = "Install the NVIDIA Container Toolkit, then start this container with the GPU (--gpus all)."
	RemedyUnraid         = "Install the Intel GPU TOP or Nvidia Driver plugin, then turn on the GPU in Portico's template."
	RemedyTrueNAS        = "Turn on NVIDIA drivers in Apps settings, then choose the GPU in Portico's app settings."
	RemedySynology       = "Map /dev/dri in Container Manager. Only Intel models have a usable GPU."
	RemedyProxmoxLXC     = "Pass the render device through to this container."
	RemedyWindows        = "Install the latest graphics driver from Intel, NVIDIA or AMD."
	RemedyRaspberryPi5   = "This board has no video encoder, so the processor converts video."
	RemedyRockchip       = "Needs a Rockchip kernel. See the guide."
)

var (
	detectOnce sync.Once
	detected   Platform
)

// Detect answers the platform this server runs on. The answer cannot change
// while the process runs, so it is read once.
func Detect() Platform {
	detectOnce.Do(func() { detected = From(readHost()) })
	return detected
}

// From resolves the platform from observed host facts. It is the seam tests use
// so each install family is checked without changing the machine running them.
func From(f hostFacts) Platform {
	if f.GOOS == "" {
		f.GOOS = runtime.GOOS
	}
	p := familyFrom(f)
	p.Container = observedContainer(f)
	return p
}

// familyFrom names the install family. Container is set separately, from what
// the process actually observes rather than from this family's usual packaging.
func familyFrom(f hostFacts) Platform {
	switch f.GOOS {
	case "darwin":
		// macOS always has a usable encoder when the GPU exists, so the owner
		// never sees a remedy line.
		return Platform{Kind: KindMacOS, Label: "macOS"}
	case "windows":
		return Platform{Kind: KindWindows, Label: "Windows", Remedy: RemedyWindows}
	}
	// Single-board computers are recognised before containers: their marker is
	// the board itself, and the install on it is the board's own.
	if board := strings.ToLower(f.Board); board != "" {
		switch {
		case strings.Contains(board, "raspberry pi 5"):
			return Platform{Kind: KindRaspberryPi5, Label: "Raspberry Pi 5", Remedy: RemedyRaspberryPi5}
		case strings.Contains(board, "rk3588"):
			return Platform{Kind: KindRockchip, Label: "Rockchip RK3588", Remedy: RemedyRockchip}
		}
	}
	if declared := normalizeKind(f.Declared); declared != "" {
		return platformFor(declared)
	}
	switch {
	case f.UnraidVersion:
		return platformFor(KindUnraid)
	case strings.Contains(strings.ToLower(f.Version), "truenas") || strings.Contains(strings.ToLower(f.Release), "truenas"):
		return platformFor(KindTrueNAS)
	case f.SynologyRelease:
		return platformFor(KindSynology)
	}
	runtimeName := strings.ToLower(strings.TrimSpace(f.ContainerRuntime))
	switch {
	case runtimeName == "lxc":
		// An LXC container says nothing about which host product runs it, so
		// only an explicit declaration or a Proxmox marker names Proxmox.
		return platformFor(KindLXC)
	case f.DockerEnv || f.ContainerEnv || runtimeName == "docker" || runtimeName == "podman" || runtimeName == "containerd":
		// A container can read the host's graphics vendor from /sys/bus/pci
		// even when no device was passed in. Without a positive vendor marker,
		// the platform stays neutral rather than claiming Intel or AMD.
		switch {
		case f.NVIDIADevice || hasVendor(f.PCIVendors, pciNVIDIA):
			return platformFor(KindDockerNVIDIA)
		case hasVendor(f.PCIVendors, pciIntel) || hasVendor(f.PCIVendors, pciAMD):
			return platformFor(KindDockerIntelAMD)
		}
		return platformFor(KindDocker)
	}
	return platformFor(KindLinux)
}

// observedContainer reports whether the process is containerized, from the
// markers and runtime it can actually see. A declared install family or a
// board marker never answers this question on its own.
func observedContainer(f hostFacts) bool {
	switch strings.ToLower(strings.TrimSpace(f.ContainerRuntime)) {
	case "docker", "podman", "containerd", "lxc":
		return true
	}
	return f.DockerEnv || f.ContainerEnv
}

// platformFor builds the published platform for one kind. An unknown kind has
// no label and no remedy, so an install that mistypes its declaration is told
// nothing it cannot act on.
func platformFor(kind string) Platform {
	switch kind {
	case KindDockerIntelAMD:
		return Platform{Kind: kind, Label: "Docker, Intel or AMD", Remedy: RemedyDockerIntelAMD}
	case KindDockerNVIDIA:
		return Platform{Kind: kind, Label: "Docker, NVIDIA", Remedy: RemedyDockerNVIDIA}
	case KindDocker:
		return Platform{Kind: kind, Label: "Docker"}
	case KindUnraid:
		return Platform{Kind: kind, Label: "Unraid", Remedy: RemedyUnraid}
	case KindTrueNAS:
		return Platform{Kind: kind, Label: "TrueNAS", Remedy: RemedyTrueNAS}
	case KindSynology:
		return Platform{Kind: kind, Label: "Synology", Remedy: RemedySynology}
	case KindProxmoxLXC:
		return Platform{Kind: kind, Label: "Proxmox LXC", Remedy: RemedyProxmoxLXC}
	case KindLXC:
		return Platform{Kind: kind, Label: "LXC"}
	case KindWindows:
		return Platform{Kind: kind, Label: "Windows", Remedy: RemedyWindows}
	case KindMacOS:
		return Platform{Kind: kind, Label: "macOS"}
	case KindRaspberryPi5:
		return Platform{Kind: kind, Label: "Raspberry Pi 5", Remedy: RemedyRaspberryPi5}
	case KindRockchip:
		return Platform{Kind: kind, Label: "Rockchip RK3588", Remedy: RemedyRockchip}
	case KindLinux:
		return Platform{Kind: kind, Label: "Linux"}
	}
	return Platform{}
}

// normalizeKind accepts a declared platform in the published spelling or with
// spaces and hyphens instead of underscores, so a template's value is not a
// trap. An undeclared platform is empty, not a guess.
func normalizeKind(declared string) string {
	value := strings.ToLower(strings.TrimSpace(declared))
	value = strings.NewReplacer("-", "_", " ", "_").Replace(value)
	switch value {
	case KindDockerIntelAMD, KindDockerNVIDIA, KindDocker, KindUnraid, KindTrueNAS, KindSynology, KindProxmoxLXC, KindLXC, KindWindows, KindMacOS, KindRaspberryPi5, KindRockchip, KindLinux:
		return value
	}
	return ""
}

// hasVendor reports whether a graphics vendor appears in the host's display
// controllers.
func hasVendor(vendors []string, want string) bool {
	for _, vendor := range vendors {
		if strings.EqualFold(strings.TrimSpace(vendor), want) {
			return true
		}
	}
	return false
}

// readHost reads the live host. Every probe is a bounded local read, and a
// missing file is an ordinary answer rather than an error.
func readHost() hostFacts {
	facts := hostFacts{GOOS: runtime.GOOS, Declared: os.Getenv(PlatformEnv)}
	facts.Board = readTrimmed("/proc/device-tree/model")
	facts.Version = readTrimmed("/etc/version")
	facts.Release = readTrimmed("/etc/os-release")
	facts.UnraidVersion = exists("/etc/unraid-version")
	facts.SynologyRelease = exists("/etc/synology-release") || exists("/etc/defaults/VERSION")
	facts.DockerEnv = exists("/.dockerenv")
	facts.ContainerEnv = exists("/run/.containerenv")
	facts.ContainerRuntime = readTrimmed("/run/systemd/container")
	if facts.ContainerRuntime == "" {
		if raw := readTrimmed("/proc/1/environ"); strings.Contains(raw, "container=lxc") {
			facts.ContainerRuntime = "lxc"
		}
	}
	facts.NVIDIADevice = exists("/dev/nvidiactl") || exists("/proc/driver/nvidia")
	facts.PCIVendors = readPCIVendors("/sys/bus/pci/devices")
	return facts
}

// readPCIVendors lists the vendors of the display controllers in a sysfs PCI
// directory. A missing directory, an unreadable entry or a card is skipped: the
// answer stays "unknown", never a guess.
func readPCIVendors(root string) []string {
	entries, e := os.ReadDir(root)
	if e != nil {
		return nil
	}
	seen := map[string]bool{}
	out := []string{}
	for _, entry := range entries {
		dir := root + "/" + entry.Name()
		class, e := os.ReadFile(dir + "/class")
		if e != nil || !strings.HasPrefix(strings.TrimSpace(string(class)), pciDisplay) {
			continue
		}
		vendor, e := os.ReadFile(dir + "/vendor")
		if e != nil {
			continue
		}
		value := strings.ToLower(strings.TrimSpace(string(vendor)))
		if value != "" && !seen[value] {
			seen[value] = true
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func readTrimmed(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
