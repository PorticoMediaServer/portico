package qual

import "fmt"

// NamespaceEvidence comes from opened namespace descriptors, not readlink text.
// Unknown or unreadable evidence is never a claim that a namespace is initial.
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
	cgroupNamespaceType uint64 = 0x02000000
	pidNamespaceType    uint64 = 0x20000000
	mountNamespaceType  uint64 = 0x00020000
)

var namespacePaths = map[string]string{
	"cgroup": "/proc/self/ns/cgroup", "pid": "/proc/self/ns/pid",
	"mount": "/proc/self/ns/mnt", "init-pid": "/proc/1/ns/pid", "init-mount": "/proc/1/ns/mnt",
}

// proveRootNamespaces requires the kernel's initial PID/cgroup namespaces and
// the same mount namespace as real PID1. This refuses ordinary containers,
// cgroup namespace remounts, unreadable PID1 provenance and unknown IDs/types.
// A mount-root string "/" alone proves nothing. There is no heuristic fallback.
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
	return nil
}
