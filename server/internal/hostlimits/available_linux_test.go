//go:build linux

package hostlimits

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAvailableMemoryClampsEachCgroupAncestor(t *testing.T) {
	root := t.TempDir()
	leaf := filepath.Join(root, "leaf")
	if err := os.Mkdir(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	for _, names := range [][2]string{{"memory.max", "memory.current"}, {"memory.limit_in_bytes", "memory.usage_in_bytes"}} {
		write := func(dir, name, value string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		write(root, names[0], "4294967296")
		write(root, names[1], "3221225472")
		write(leaf, names[0], "2147483648")
		write(leaf, names[1], "536870912")
		available, total, err := availableInHierarchy(8<<30, 16<<30, leaf, root, names[0], names[1])
		if err != nil || available != 1<<30 || total != 2<<30 {
			t.Fatal(available, total, err)
		}
		write(root, names[1], "5368709120")
		available, _, err = availableInHierarchy(8<<30, 16<<30, leaf, root, names[0], names[1])
		if err != nil || available != 0 {
			t.Fatal(available, err)
		}
		if err = os.Remove(filepath.Join(leaf, names[1])); err != nil {
			t.Fatal(err)
		}
		if _, _, err = availableInHierarchy(8<<30, 16<<30, leaf, root, names[0], names[1]); err == nil {
			t.Fatal("unreadable usage accepted")
		}
	}
}

func TestAvailableMemoryRecognizesRealV2Root(t *testing.T) {
	root := t.TempDir()
	available, total, err := availableInHierarchy(2<<30, 4<<30, root, root, "memory.max", "memory.current")
	if err != nil || available != 2<<30 || total != 4<<30 {
		t.Fatal(available, total, err)
	}
	if err = os.WriteFile(filepath.Join(root, "cgroup.type"), []byte("domain"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = availableInHierarchy(2<<30, 4<<30, root, root, "memory.max", "memory.current"); err == nil {
		t.Fatal("missing limit on namespaced cgroup accepted")
	}
}
