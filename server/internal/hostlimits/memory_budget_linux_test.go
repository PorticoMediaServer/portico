//go:build linux

package hostlimits

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMemoryHierarchyHonorsAncestorAndMountBoundaries(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	leaf := filepath.Join(parent, "leaf")
	if err := os.MkdirAll(leaf, 0700); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{root: "1073741824", parent: "67108864", leaf: "max"} {
		if err := os.WriteFile(filepath.Join(path, "memory.max"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if limit, ok := hierarchyMemoryLimit(leaf, root); !ok || limit != 64<<20 {
		t.Fatalf("leaf max bypassed ancestor64MiB: %d %v", limit, ok)
	}
	if _, ok := hierarchyMemoryLimit(filepath.Join(root, "..", "outside"), root); ok {
		t.Fatal("escaped the cgroup mount")
	}
	for _, tc := range []struct{ process, mountRoot, want string }{
		{"/parent/leaf", "/", "/parent/leaf"},
		{"/container/parent/leaf", "/container", "/parent/leaf"},
		{"/parent/leaf", "/container", "/parent/leaf"},
		{"/", "/container", ""},
		{"/../outside", "/", "reject"},
	} {
		mount := "1 0 0:1 " + tc.mountRoot + " /sys/fs/cgroup rw - cgroup2 cgroup rw\n"
		dir, mountPoint, ok := memoryHierarchy("0::"+tc.process+"\n", mount)
		if tc.want == "reject" {
			if ok {
				t.Fatal("unsafe namespace process path accepted")
			}
			continue
		}
		if !ok || dir != "/sys/fs/cgroup"+tc.want || mountPoint != "/sys/fs/cgroup" {
			t.Fatalf("namespace mapping %q %q = %q %q %v", tc.process, tc.mountRoot, dir, mountPoint, ok)
		}
	}
}

func TestPhysicalMemoryParser(t *testing.T) {
	for _, tc := range []struct {
		text string
		want uint64
	}{
		{"MemAvailable: 1 kB\nMemTotal: 262144 kB\n", 256 << 20},
		{"MemTotal: 0 kB\n", 0},
		{"MemTotal: -1 kB\n", 0},
		{"MemTotal: 42 bytes\n", 0},
		{"MemTotal: 18446744073709551615 kB\n", 0},
		{"missing\n", 0},
	} {
		got, ok := parsePhysicalMemory(strings.NewReader(tc.text))
		if got != tc.want || ok != (tc.want > 0) {
			t.Fatalf("parse %q = %d,%v want %d", tc.text, got, ok, tc.want)
		}
	}
}
