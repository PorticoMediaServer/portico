package qual

import (
	"errors"
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
	Namespaces      map[string]NamespaceEvidence `json:"namespaces_before"`
	NamespacesAfter map[string]NamespaceEvidence `json:"namespaces_after"`
	ProcessPaths    []string                     `json:"enumerated_process_paths"`
	ThreadPaths     []string                     `json:"enumerated_thread_paths"`
	Version         int                          `json:"version"`
	Kind            string                       `json:"kind"`
	At              time.Time                    `json:"at"`
	GOOS            string                       `json:"goos"`
	GOARCH          string                       `json:"goarch"`
	CPUs            int                          `json:"runtime_cpus"`
	MaxProcs        int                          `json:"gomaxprocs"`
	Files           map[string]string            `json:"files"`
	Missing         map[string]string            `json:"missing"`
	Errors          map[string]TelemetryError    `json:"read_errors"`
	Topology        map[string]string            `json:"cpu_topology"`
	MountRoot       string                       `json:"cgroup_mount_root"`
	MountPoint      string                       `json:"cgroup_mount_point"`
	Processes       map[string]string            `json:"process_names"`
	Threads         map[string]string            `json:"thread_schedstat"`
	Cgroups         []CgroupEvidence             `json:"cgroups"`
	GoMemory        GoMemoryEvidence             `json:"go_memory"`
}

// CgroupEvidence includes each ancestor, since a leaf's "max" can inherit a
// stricter limit. Paths are relative to the visible cgroup-v2 mount. An empty
// list or a missing counter must not be interpreted as unlimited resources.
type CgroupEvidence struct {
	Path    string                    `json:"path"`
	Files   map[string]string         `json:"files"`
	Missing map[string]string         `json:"missing"`
	Errors  map[string]TelemetryError `json:"read_errors"`
}

// TelemetryError distinguishes expected nonexistence from unreadable/unknown data.
type TelemetryError struct {
	Kind      string `json:"kind"`
	Operation string `json:"operation"`
	Detail    string `json:"detail"`
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
func CaptureHost() HostEvidence {
	before := captureNamespaces()
	h := captureHost(os.DirFS("/"))
	h.Namespaces = before
	h.NamespacesAfter = captureNamespaces()
	return h
}

func captureHost(root fs.FS) HostEvidence {
	h := HostEvidence{
		Version: 3, Kind: "host-evidence-only", At: time.Now().UTC(),
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, CPUs: runtime.NumCPU(),
		MaxProcs: runtime.GOMAXPROCS(0), Files: map[string]string{}, Missing: map[string]string{},
		Errors: map[string]TelemetryError{}, Topology: map[string]string{}, Processes: map[string]string{}, Threads: map[string]string{}, Cgroups: []CgroupEvidence{},
	}
	for _, name := range []string{
		"sys/devices/system/cpu/online", "sys/devices/system/cpu/present", "etc/os-release", "proc/cpuinfo", "proc/meminfo", "proc/stat", "proc/loadavg", "proc/diskstats",
		"proc/sys/kernel/osrelease", "proc/sys/kernel/hostname", "proc/sys/kernel/random/boot_id",
		"proc/self/status", "proc/self/stat", "proc/self/cgroup", "proc/self/mountinfo",
		"proc/pressure/cpu", "proc/pressure/io", "proc/pressure/memory",
	} {
		recordFile(root, name, h.Files, h.Missing, h.Errors)
	}
	for _, glob := range []struct {
		pattern string
		target  map[string]string
	}{
		{"sys/devices/system/cpu/cpu[0-9]*/topology/core_id", h.Topology},
		{"sys/devices/system/cpu/cpu[0-9]*/topology/physical_package_id", h.Topology},
		{"sys/devices/system/cpu/cpu[0-9]*/topology/thread_siblings_list", h.Topology},
	} {
		files, err := fs.Glob(root, glob.pattern)
		if err != nil || len(files) == 0 {
			h.Missing[glob.pattern] = "no visible entries"
		}
		for _, name := range files {
			recordFile(root, name, glob.target, h.Missing, h.Errors)
		}
	}
	for _, set := range []struct {
		directory, suffix string
		paths             *[]string
		into              map[string]string
	}{
		{"proc", "comm", &h.ProcessPaths, h.Processes}, {"proc/self/task", "schedstat", &h.ThreadPaths, h.Threads},
	} {
		entries, err := telemetryDirectory(root, set.directory)
		if err != nil {
			noteTelemetryError(set.directory, err, "enumerate", h.Missing, h.Errors)
		}
		for _, entry := range entries {
			if _, err := unsigned(entry.Name()); err != nil {
				continue
			}
			name := path.Join(set.directory, entry.Name(), set.suffix)
			*set.paths = append(*set.paths, name)
			recordFile(root, name, set.into, h.Missing, h.Errors)
		}
	}
	leaf, err := cgroupDirectory(h.Files["proc/self/cgroup"], h.Files["proc/self/mountinfo"])
	if err != nil {
		h.Missing["cgroup-v2"] = err.Error()
	} else {
		h.MountRoot, h.MountPoint = leaf.root, leaf.mount
		for current := leaf.directory; ; current = path.Dir(current) {
			cg := CgroupEvidence{Path: current, Files: map[string]string{}, Missing: map[string]string{}, Errors: map[string]TelemetryError{}}
			for _, name := range []string{
				"cgroup.controllers", "cpu.max", "cpu.stat", "cpu.pressure", "cpuset.cpus.effective",
				"memory.max", "memory.current", "memory.peak", "memory.stat", "memory.events",
				"memory.swap.max", "memory.swap.current", "memory.pressure", "io.stat", "io.max", "io.pressure",
			} {
				recordFile(root, path.Join(current, name), cg.Files, cg.Missing, cg.Errors)
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

func recordFile(root fs.FS, name string, into, missing map[string]string, faults map[string]TelemetryError) {
	fail := func(err error, operation string) { noteTelemetryError(name, err, operation, missing, faults) }
	f, err := root.Open(name)
	if err != nil {
		fail(err, "open")
		return
	}
	b, readErr := io.ReadAll(io.LimitReader(f, maxTelemetryFile+1))
	closeErr := f.Close()
	// Retain partial raw bytes, but a read/close error always invalidates them.
	if len(b) > 0 {
		into[name] = string(b[:min(len(b), maxTelemetryFile)])
	}
	if readErr != nil {
		fail(readErr, "read")
		return
	}
	if closeErr != nil {
		fail(closeErr, "close")
		return
	}
	if len(b) > maxTelemetryFile {
		fail(fmt.Errorf("exceeds %d-byte telemetry limit; not silently truncated", maxTelemetryFile), "oversized")
		return
	}
	into[name] = string(b)
}

type cgroupLocation struct{ root, mount, directory string }

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
				root:      root,
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

func noteTelemetryError(name string, err error, operation string, missing map[string]string, faults map[string]TelemetryError) {
	kind := operation
	if errors.Is(err, fs.ErrNotExist) {
		kind = "not-exist"
	} else if errors.Is(err, fs.ErrPermission) {
		kind = "permission"
	}
	missing[name] = err.Error()
	faults[name] = TelemetryError{Kind: kind, Operation: operation, Detail: err.Error()}
}

// ReadDir helpers in io/fs ignore directory Close errors; required enumeration
// joins read and close explicitly, retaining any partial entries with the error.
func telemetryDirectory(root fs.FS, name string) ([]fs.DirEntry, error) {
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	dir, ok := f.(fs.ReadDirFile)
	if !ok {
		return nil, errors.Join(fmt.Errorf("not a directory reader"), f.Close())
	}
	entries, readErr := dir.ReadDir(-1)
	return entries, errors.Join(readErr, f.Close())
}
