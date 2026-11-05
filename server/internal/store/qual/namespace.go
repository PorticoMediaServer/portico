package qual

import "fmt"

// NamespaceEvidence records opened namespace descriptors and the proc-root /
// proc-init directory descriptors that structurally anchor their source. Type
// is zero for directories. This detects accidental mislabelling on dispatched
// hosts, not deliberate procfs forgery. Unknown/unreadable evidence refuses.
type NamespaceEvidence struct {
	Inode      uint64   `json:"inode"`
	Device     uint64   `json:"device"`
	Filesystem uint64   `json:"filesystem"`
	Type       uint64   `json:"type"`
	Errors     []string `json:"errors,omitempty"`
}

const (
	// Source-reviewed reserved initial namespace inode IDs, not IDs of PID 1 in
	// an arbitrary container. Dynamic namespace IDs occupy another range.
	// https://github.com/torvalds/linux/blob/v6.8/include/linux/proc_ns.h
	initialCgroupInode  uint64 = 0xeffffffb
	initialPIDInode     uint64 = 0xeffffffc
	nsfsMagic           uint64 = 0x6e736673
	procfsMagic         uint64 = 0x9fa0 // Linux PROC_SUPER_MAGIC
	cgroupNamespaceType uint64 = 0x02000000
	pidNamespaceType    uint64 = 0x20000000
	mountNamespaceType  uint64 = 0x00020000
)

var namespacePaths = map[string]string{
	"cgroup": "/proc/self/ns/cgroup", "pid": "/proc/self/ns/pid",
	"mount": "/proc/self/ns/mnt", "init-pid": "/proc/1/ns/pid", "init-mount": "/proc/1/ns/mnt",
}

var procfsPaths = map[string]string{"proc-root": "/proc", "proc-init": "/proc/1"}

// proveRootNamespaces requires the kernel's initial PID/cgroup namespaces and
// matching readable mount handles from /proc/self and /proc/1. Together with
// same-instance procfs checks this detects accidental container/private views;
// it does not authenticate PID1 against deliberate procfs forgery. A mount-root
// string "/" alone proves nothing. There is no inferred-unlimited fallback.
func proveRootNamespaces(before, after map[string]NamespaceEvidence) error {
	for key := range namespacePaths {
		a, ok := before[key]
		b, endOK := after[key]
		want := mountNamespaceType
		if key == "cgroup" {
			want = cgroupNamespaceType
		} else if key == "pid" || key == "init-pid" {
			want = pidNamespaceType
		}
		if !ok || !endOK || len(a.Errors) > 0 || len(b.Errors) > 0 || a.Inode == 0 || a.Device == 0 || a.Filesystem != nsfsMagic || a.Type != want {
			return fmt.Errorf("unproven real hierarchy root: missing/unreadable/non-namespace %s descriptor", key)
		}
		if a.Inode != b.Inode || a.Device != b.Device || a.Filesystem != b.Filesystem || a.Type != b.Type {
			return fmt.Errorf("unproven real hierarchy root: namespace changed during capture (%s)", key)
		}
	}
	if before["cgroup"].Inode != initialCgroupInode || before["pid"].Inode != initialPIDInode || before["init-pid"].Inode != initialPIDInode {
		return fmt.Errorf("unproven real hierarchy root: noninitial cgroup/PID namespace or container")
	}
	a, b := before["mount"], before["init-mount"]
	if a.Device != b.Device || a.Inode != b.Inode {
		return fmt.Errorf("unproven real hierarchy root: private mount namespace/container view")
	}
	return proveProcfs(before, after)
}

// Cheap structural source check only. A deliberately forged same-procfs view
// is outside this collector's threat model; no further anti-forgery is claimed.
func proveProcfs(before, after map[string]NamespaceEvidence) error {
	for _, snapshot := range []map[string]NamespaceEvidence{before, after} {
		for key := range procfsPaths {
			n, ok := snapshot[key]
			if !ok || len(n.Errors) > 0 || n.Device == 0 || n.Filesystem != procfsMagic {
				return fmt.Errorf("unproven procfs structure: missing/unreadable/non-procfs %s descriptor", key)
			}
		}
		if snapshot["proc-root"].Device != snapshot["proc-init"].Device {
			return fmt.Errorf("unproven procfs structure: /proc and /proc/1 are on different instances")
		}
	}
	return nil
}
