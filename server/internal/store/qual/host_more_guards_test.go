package qual

import (
	"strings"
	"testing"
)

// Each control has only the named defect in an otherwise valid host fixture.
func TestHostUnknownCgroupCPUGuard(t *testing.T) {
	h := validHost(t)
	setCG(&h, 2, "cpuset.cpus.effective", "0-4")
	rejectHost(t, h)
}
func TestHostIOStatRequiredFieldsGuard(t *testing.T) {
	h := validHost(t)
	setCG(&h, 1, "io.stat", "8:0 rbytes=4096 wbytes=8192 rios=1 discarded=0")
	rejectHost(t, h)
}
func TestHostProcStatRequiredCountersGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/stat"] = strings.Replace(h.Files["proc/stat"], "ctxt 1000\n", "", 1)
	rejectHost(t, h)
}
func TestHostLoadAverageRelationGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/loadavg"] = "0.50 0.25 0.10 26/25 123"
	rejectHost(t, h)
}
func TestHostPositiveThreadsGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/self/status"] = strings.Replace(h.Files["proc/self/status"], "Threads: 1", "Threads: 0", 1)
	rejectHost(t, h)
}
func TestHostDuplicateCPUInfoGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/cpuinfo"] = strings.Replace(h.Files["proc/cpuinfo"], "model name :", "processor : 0\nmodel name :", 1)
	rejectHost(t, h)
}
func TestHostAncestorOrderGuard(t *testing.T) {
	h := validHost(t)
	h.Cgroups[0], h.Cgroups[1] = h.Cgroups[1], h.Cgroups[0]
	rejectHost(t, h)
}

func TestHostProcfsMagicGuard(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		for key := range procfsPaths {
			h := validHost(t)
			m := h.Namespaces
			if phase == "after" {
				m = h.NamespacesAfter
			}
			n := m[key]
			n.Filesystem = 0x01021994 // synthetic tmpfs directory over /proc/1
			m[key] = n
			rejectHost(t, h)
		}
	}
}
func TestHostProcfsDeviceGuard(t *testing.T) {
	for _, phase := range []string{"before", "after"} {
		h := validHost(t)
		m := h.Namespaces
		if phase == "after" {
			m = h.NamespacesAfter
		}
		n := m["proc-init"]
		n.Device++ // still procfs, but a different instance
		m["proc-init"] = n
		rejectHost(t, h)
	}
}
func TestHostMountHandlesReadableGuard(t *testing.T) {
	for _, key := range []string{"mount", "init-mount"} {
		h := validHost(t)
		for _, m := range []map[string]NamespaceEvidence{h.Namespaces, h.NamespacesAfter} {
			n := m[key]
			n.Errors = []string{"descriptor read/close failed"}
			m[key] = n
		}
		rejectHost(t, h)
	}
}
func TestHostProcfsEvidenceRequired(t *testing.T) {
	for key := range procfsPaths {
		h := validHost(t)
		delete(h.Namespaces, key)
		rejectHost(t, h)
		h = validHost(t)
		n := h.NamespacesAfter[key]
		n.Errors = []string{"open: permission denied"}
		h.NamespacesAfter[key] = n
		rejectHost(t, h)
	}
}

func TestHostSyntheticProcInitStructuralRefusal(t *testing.T) {
	for _, profile := range []string{"standard", "legacy8", "ram2gb"} {
		h := validHost(t)
		if profile == "standard" {
			setCG(&h, 0, "cpu.max", "max 100000")
			setCG(&h, 0, "memory.max", "max")
		} else if profile == "ram2gb" {
			setCG(&h, 0, "memory.max", "2000000000")
		}
		for _, m := range []map[string]NamespaceEvidence{h.Namespaces, h.NamespacesAfter} {
			// Both genuine namespace handles point at the SAME private mount,
			// reproducing the paired-symlink gap rather than changing one handle.
			for _, key := range []string{"mount", "init-mount"} {
				n := m[key]
				n.Inode = 9000000000
				m[key] = n
			}
			n := m["proc-init"]
			n.Filesystem = 0x01021994
			n.Device++
			m["proc-init"] = n
		}
		if problems := ValidateHost(h, profile); len(problems) == 0 {
			t.Fatalf("synthetic /proc/1 qualified %s", profile)
		}
	}
}
