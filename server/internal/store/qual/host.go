package qual

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime"
	"strings"
	"time"
)

// HostEvidence is telemetry, never a performance qualification result. Capture
// it outside writer custody and retain missing/unreadable counters explicitly.
// It deliberately excludes environment variables and process command lines.
type HostEvidence struct {
	Version   int               `json:"version"`
	Kind      string            `json:"kind"`
	At        time.Time         `json:"at"`
	GOOS      string            `json:"goos"`
	GOARCH    string            `json:"goarch"`
	CPUs      int               `json:"runtime_cpus"`
	MaxProcs  int               `json:"gomaxprocs"`
	Files     map[string]string `json:"files"`
	Missing   map[string]string `json:"missing"`
	Processes map[string]string `json:"process_names"`
	Threads   map[string]string `json:"thread_schedstat"`
	Cgroups   []CgroupEvidence  `json:"cgroups"`
	GoMemory  GoMemoryEvidence  `json:"go_memory"`
}

// CgroupEvidence includes each ancestor, since a leaf's "max" can inherit a
// stricter limit. Paths are relative to the visible cgroup-v2 mount. An empty
// list or a missing counter must not be interpreted as unlimited resources.
type CgroupEvidence struct {
	Path    string            `json:"path"`
	Files   map[string]string `json:"files"`
	Missing map[string]string `json:"missing"`
}

type GoMemoryEvidence struct {
	HeapAlloc     uint64  `json:"heap_alloc_bytes"`
	HeapSys       uint64  `json:"heap_sys_bytes"`
	StackSys      uint64  `json:"stack_sys_bytes"`
	Sys           uint64  `json:"sys_bytes"`
	GCCycles      uint32  `json:"gc_cycles"`
	GCPauseTotal  uint64  `json:"gc_pause_total_ns"`
	GCCPUFraction float64 `json:"gc_cpu_fraction"`
}

// CaptureHost records Linux procfs/cgroup-v2 evidence from this process's own
// namespace. It opens no database, runs no subprocess, changes no limits, and
// never claims the machine is quiet from a zero/unavailable steal counter.
// Call at start/end and use a separately budgeted cadence for diagnostic samples;
// full proc enumeration is not intended for the hot path or every SQL command.
func CaptureHost() HostEvidence { return captureHost(os.DirFS("/")) }

func captureHost(root fs.FS) HostEvidence {
	h := HostEvidence{
		Version: 1, Kind: "host-evidence-only", At: time.Now().UTC(),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(),
		MaxProcs: runtime.GOMAXPROCS(0), Files: map[string]string{}, Missing: map[string]string{},
		Processes: map[string]string{}, Threads: map[string]string{}, Cgroups: []CgroupEvidence{},
	}
	for _, name := range []string{
		"etc/os-release", "proc/cpuinfo", "proc/meminfo", "proc/stat", "proc/loadavg", "proc/diskstats",
		"proc/sys/kernel/osrelease", "proc/sys/kernel/hostname", "proc/sys/kernel/random/boot_id",
		"proc/self/status", "proc/self/stat", "proc/self/cgroup", "proc/self/mountinfo",
		"proc/pressure/cpu", "proc/pressure/io", "proc/pressure/memory",
	} {
		recordFile(root, name, h.Files, h.Missing)
	}
	for _, glob := range []struct {
		pattern string
		target  map[string]string
	}{
		{"proc/[0-9]*/comm", h.Processes},
		{"proc/self/task/[0-9]*/schedstat", h.Threads},
	} {
		files, err := fs.Glob(root, glob.pattern)
		if err != nil || len(files) == 0 {
			h.Missing[glob.pattern] = "no visible entries"
		}
		for _, name := range files {
			recordFile(root, name, glob.target, h.Missing)
		}
	}
	leaf, err := cgroupDirectory(h.Files["proc/self/cgroup"], h.Files["proc/self/mountinfo"])
	if err != nil {
		h.Missing["cgroup-v2"] = err.Error()
	} else {
		for current := leaf.directory; ; current = path.Dir(current) {
			cg := CgroupEvidence{Path: current, Files: map[string]string{}, Missing: map[string]string{}}
			for _, name := range []string{
				"cpu.max", "cpu.stat", "cpu.pressure", "cpuset.cpus.effective",
				"memory.max", "memory.current", "memory.peak", "memory.stat", "memory.events",
				"memory.swap.max", "memory.swap.current", "memory.pressure", "io.stat", "io.max", "io.pressure",
			} {
				recordFile(root, path.Join(current, name), cg.Files, cg.Missing)
			}
			h.Cgroups = append(h.Cgroups, cg)
			if current == leaf.mount {
				break
			}
		}
	}
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	h.GoMemory = GoMemoryEvidence{
		HeapAlloc: mem.HeapAlloc, HeapSys: mem.HeapSys, StackSys: mem.StackSys, Sys: mem.Sys,
		GCCycles: mem.NumGC, GCPauseTotal: mem.PauseTotalNs, GCCPUFraction: mem.GCCPUFraction,
	}
	return h
}

const maxTelemetryFile = 256 << 10

func recordFile(root fs.FS, name string, into, missing map[string]string) {
	f, err := root.Open(name)
	if err != nil {
		missing[name] = err.Error()
		return
	}
	b, readErr := io.ReadAll(io.LimitReader(f, maxTelemetryFile+1))
	closeErr := f.Close()
	if readErr != nil {
		missing[name] = readErr.Error()
		return
	}
	if closeErr != nil {
		missing[name] = closeErr.Error()
		return
	}
	if len(b) > maxTelemetryFile {
		missing[name] = fmt.Sprintf("exceeds %d-byte telemetry limit; not silently truncated", maxTelemetryFile)
		return
	}
	into[name] = string(b)
}

type cgroupLocation struct{ mount, directory string }

// Resolve the actual mount root, including a namespaced/subtree cgroup mount.
// Do not guess /sys/fs/cgroup, and never let a proc path escape that mount.
func cgroupDirectory(membership, mounts string) (cgroupLocation, error) {
	var member string
	for _, line := range strings.Split(membership, "\n") {
		if strings.HasPrefix(line, "0::") {
			if member != "" {
				return cgroupLocation{}, fmt.Errorf("multiple unified cgroup memberships")
			}
			member = strings.TrimPrefix(line, "0::")
		}
	}
	if !cleanAbsolute(member) {
		return cgroupLocation{}, fmt.Errorf("missing or unclean unified cgroup path")
	}
	var selected cgroupLocation
	best := -1
	for _, line := range strings.Split(mounts, "\n") {
		parts := strings.SplitN(line, " - ", 2)
		if len(parts) != 2 || !strings.HasPrefix(parts[1], "cgroup2 ") {
			continue
		}
		fields := strings.Fields(parts[0])
		if len(fields) < 6 {
			continue
		}
		root, mount := unescapeMount(fields[3]), unescapeMount(fields[4])
		if !cleanAbsolute(root) || !cleanAbsolute(mount) || !within(member, root) {
			continue
		}
		if len(root) > best {
			best = len(root)
			selected = cgroupLocation{
				mount:     strings.TrimPrefix(mount, "/"),
				directory: strings.TrimPrefix(path.Join(mount, strings.TrimPrefix(member, root)), "/"),
			}
		}
	}
	if best < 0 || selected.mount == "" {
		return cgroupLocation{}, fmt.Errorf("no matching visible cgroup-v2 mount")
	}
	return selected, nil
}

func cleanAbsolute(s string) bool { return strings.HasPrefix(s, "/") && path.Clean(s) == s }
func within(p, root string) bool  { return root == "/" || p == root || strings.HasPrefix(p, root+"/") }
func unescapeMount(s string) string {
	return strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`).Replace(s)
}
