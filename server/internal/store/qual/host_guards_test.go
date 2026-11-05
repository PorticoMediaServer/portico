package qual

import (
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

// Each of the nine controls changes only the named claim in an otherwise valid
// profile. In particular NaN retains BOTH pressure populations, and max 0 is an
// ancestor above the valid 2-CPU capped leaf. Mutation results are reproducible
// independently, not incidental kills from a second broken fixture field.
func TestHostArchitectureGuard(t *testing.T) {
	h := validHost(t)
	h.GOARCH = "arm64"
	rejectHost(t, h)
}
func TestHostOnlineAgreementGuard(t *testing.T) {
	h := validHost(t)
	h.Files["sys/devices/system/cpu/present"] = "0-4"
	rejectHost(t, h)
}
func TestHostProcStatAgreementGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/stat"] = strings.Replace(h.Files["proc/stat"], "cpu3 25 0 12 250 2 0 1 1 0 0\n", "", 1)
	rejectHost(t, h)
}
func TestHostSMTSelfSubsetGuard(t *testing.T) {
	h := validHost(t)
	h.Topology["sys/devices/system/cpu/cpu0/topology/thread_siblings_list"] = "2-3"
	rejectHost(t, h)
}
func TestHostPhysicalRAMGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/meminfo"] = "MemTotal: 32768000 kB"
	rejectHost(t, h)
}
func TestHostControllersGuard(t *testing.T) {
	h := validHost(t)
	setCG(&h, 1, "cgroup.controllers", "cpu cpuset memory pids")
	rejectHost(t, h)
}
func TestHostThreadNumericGuard(t *testing.T) {
	h := validHost(t)
	h.Threads[h.ThreadPaths[0]] = "1000 bad 3"
	rejectHost(t, h)
}
func TestHostPressureAverageGuard(t *testing.T) {
	h := validHost(t)
	key := path.Join(h.Cgroups[1].Path, "io.pressure")
	h.Cgroups[1].Files[key] = strings.Replace(h.Cgroups[1].Files[key], "avg10=1.25", "avg10=NaN", 1)
	rejectHost(t, h)
}
func TestHostAncestorCPUPeriodGuard(t *testing.T) {
	h := validHost(t)
	setCG(&h, 1, "cpu.max", "max 0")
	rejectHost(t, h)
}

func TestHostNamespaceRootGuard(t *testing.T) {
	for _, key := range []string{"cgroup", "pid", "init-pid", "mount", "init-mount"} {
		t.Run(key, func(t *testing.T) {
			h := validHost(t)
			for _, m := range []map[string]NamespaceEvidence{h.Namespaces, h.NamespacesAfter} {
				n := m[key]
				n.Inode = 9000000000
				m[key] = n
			}
			rejectHost(t, h)
		})
	}
	for _, change := range []func(*HostEvidence){
		func(h *HostEvidence) { h.Namespaces = nil },
		func(h *HostEvidence) { delete(h.NamespacesAfter, "mount") },
		func(h *HostEvidence) {
			n := h.Namespaces["cgroup"]
			n.Errors = []string{"open: permission denied"}
			h.Namespaces["cgroup"] = n
		},
		func(h *HostEvidence) {
			n := h.Namespaces["cgroup"]
			n.Type = pidNamespaceType
			h.Namespaces["cgroup"] = n
		},
		func(h *HostEvidence) { n := h.Namespaces["cgroup"]; n.Filesystem = 0; h.Namespaces["cgroup"] = n },
		func(h *HostEvidence) { n := h.NamespacesAfter["mount"]; n.Inode++; h.NamespacesAfter["mount"] = n },
	} {
		h := validHost(t)
		change(&h)
		rejectHost(t, h)
	}
}
func TestHostRootReadableControlGuard(t *testing.T) {
	for name, s := range map[string]string{"cpu.max": "max 100000", "memory.max": "max", "memory.swap.max": "max", "io.max": ""} {
		h := validHost(t)
		setCG(&h, 2, name, s)
		rejectHost(t, h)
	}
}
func TestHostRemountedNamespaceRoot(t *testing.T) {
	h := validHost(t)
	rootPresent(&h)
	for _, m := range []map[string]NamespaceEvidence{h.Namespaces, h.NamespacesAfter} {
		for _, key := range []string{"cgroup", "pid", "init-pid", "mount"} {
			n := m[key]
			n.Inode += 100000
			m[key] = n
		}
	}
	// Root path and readable unlimited control files mimic unshare+remount.
	// They cannot prove the stricter parent outside the visible namespace.
	rejectHost(t, h)
}
func TestHostIOCompleteRecordGuard(t *testing.T) {
	keys := []string{"rbps", "wbps", "riops", "wiops"}
	for _, omit := range keys {
		h := validHost(t)
		line := "8:0"
		for _, key := range keys {
			if key != omit {
				line += " " + key + "=max"
			}
		}
		setCG(&h, 1, "io.max", line)
		rejectHost(t, h)
	}
	for _, line := range []string{"8:0", "8:0 rbps=max rbps=max riops=max wiops=max", "8:0 rbps=max wbps=max riops=max wiops=max extra=max"} {
		h := validHost(t)
		setCG(&h, 1, "io.max", line)
		rejectHost(t, h)
	}
}
func TestHostSMTCoherenceGuard(t *testing.T) {
	for _, change := range []func(*HostEvidence){
		func(h *HostEvidence) { h.Topology["sys/devices/system/cpu/cpu0/topology/thread_siblings_list"] = "0,2" },
		func(h *HostEvidence) { h.Topology["sys/devices/system/cpu/cpu1/topology/core_id"] = "1" },
		func(h *HostEvidence) { h.Topology["sys/devices/system/cpu/cpu1/topology/physical_package_id"] = "1" },
		func(h *HostEvidence) {
			h.Files["proc/cpuinfo"] = strings.Replace(h.Files["proc/cpuinfo"], "core id : 0", "core id : 1", 1)
		},
		func(h *HostEvidence) {
			h.Files["proc/cpuinfo"] = strings.Replace(h.Files["proc/cpuinfo"], "physical id : 0", "physical id : 1", 1)
		},
	} {
		h := validHost(t)
		change(&h)
		rejectHost(t, h)
	}
}

type faultFile struct {
	data        *strings.Reader
	read, close bool
}

func (f faultFile) Stat() (fs.FileInfo, error) { return nil, fs.ErrInvalid }

func (f faultFile) Read(b []byte) (int, error) {
	n, e := f.data.Read(b)
	if f.read && e == io.EOF {
		return n, fmt.Errorf("injected read failure after valid bytes")
	}
	return n, e
}
func (f faultFile) Close() error {
	if f.close {
		return fmt.Errorf("injected close failure")
	}
	return nil
}

type faultFS struct {
	data        string
	read, close bool
}

func (f faultFS) Open(string) (fs.File, error) {
	return faultFile{data: strings.NewReader(f.data), read: f.read, close: f.close}, nil
}
func TestHostProcessReadErrorsGuard(t *testing.T) {
	for _, thread := range []bool{false, true} {
		for _, operation := range []string{"read", "close"} {
			t.Run(fmt.Sprintf("thread%t/%s", thread, operation), func(t *testing.T) {
				h := validHost(t)
				name, values := h.ProcessPaths[0], h.Processes
				if thread {
					name, values = h.ThreadPaths[0], h.Threads
				}
				data := values[name]
				recordFile(faultFS{data: data, read: operation == "read", close: operation == "close"}, name, values, h.Missing, h.Errors)
				if values[name] != data || h.Errors[name].Operation != operation {
					t.Fatal("control did not retain valid bytes plus injected fault")
				}
				rejectHost(t, h)
			})
		}
	}
}
func TestHostUnavailableThreadAlongsideReadable(t *testing.T) {
	h := validHost(t)
	name := "proc/self/task/456/schedstat"
	h.ThreadPaths = append(h.ThreadPaths, name)
	h.Errors[name] = TelemetryError{Kind: "permission", Operation: "open", Detail: "injected"}
	rejectHost(t, h)
}
func TestHostEnumerationErrorGuard(t *testing.T) {
	for _, directory := range []string{"proc", "proc/self/task"} {
		h := validHost(t)
		h.Errors[directory] = TelemetryError{Kind: "read", Operation: "enumerate", Detail: "partial directory read"}
		rejectHost(t, h)
	}
}
func TestHostMissingEnumeratedEntry(t *testing.T) {
	h := validHost(t)
	delete(h.Processes, h.ProcessPaths[0])
	rejectHost(t, h)
	h = validHost(t)
	h.ThreadPaths = nil
	rejectHost(t, h)
}

type directoryFaultFS struct {
	fs.FS
	close bool
}

func (f directoryFaultFS) Open(name string) (fs.File, error) {
	fh, e := f.FS.Open(name)
	if e != nil {
		return nil, e
	}
	return directoryFault{ReadDirFile: fh.(fs.ReadDirFile), close: f.close}, nil
}

type directoryFault struct {
	fs.ReadDirFile
	close bool
}

func (f directoryFault) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, e := f.ReadDirFile.ReadDir(n)
	if !f.close {
		e = fmt.Errorf("injected directory read error")
	}
	return entries, e
}
func (f directoryFault) Close() error {
	_ = f.ReadDirFile.Close()
	if f.close {
		return fmt.Errorf("injected directory close error")
	}
	return nil
}
func TestTelemetryDirectoryRetainsReadAndCloseErrors(t *testing.T) {
	for _, close := range []bool{false, true} {
		entries, e := telemetryDirectory(directoryFaultFS{FS: fstest.MapFS{"proc/123/comm": &fstest.MapFile{Data: []byte("fixture")}}, close: close}, "proc")
		if e == nil || len(entries) != 1 {
			t.Fatalf("partial entries/errors lost: %v %v", entries, e)
		}
	}
}
