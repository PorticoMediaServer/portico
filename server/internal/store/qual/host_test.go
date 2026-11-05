package qual

import (
	"path"
	"strings"
	"testing"
	"testing/fstest"
)

func TestCgroupMountResolution(t *testing.T) {
	for _, tt := range []struct{ name, membership, mounts, directory, mount string }{
		{"root", "0::/actions/job\n", "29 1 0:27 / /sys/fs/cgroup rw - cgroup2 cgroup rw", "sys/fs/cgroup/actions/job", "sys/fs/cgroup"},
		{"subtree", "0::/actions/job\n", "29 1 0:27 /actions /limits rw - cgroup2 cgroup rw", "limits/job", "limits"},
		{"exact-root", "0::/actions\n", "29 1 0:27 /actions /limits rw - cgroup2 cgroup rw", "limits", "limits"},
		{"escaped", "0::/actions/job\n", `29 1 0:27 /actions /my\040limits rw - cgroup2 cgroup rw`, "my limits/job", "my limits"},
		{"specific", "0::/actions/job\n", "29 1 0:27 / /general rw - cgroup2 cgroup rw\n30 1 0:27 /actions /specific rw - cgroup2 cgroup rw", "specific/job", "specific"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cgroupDirectory(tt.membership, tt.mounts)
			if err != nil || got.directory != tt.directory || got.mount != tt.mount {
				t.Fatalf("got %+v, %v", got, err)
			}
		})
	}
}

func TestCgroupBadPathsAreExplicitlyUnavailable(t *testing.T) {
	for _, member := range []string{"", "0::relative", "0::/../outside", "0::/actions/../outside", "0::/actionssibling", "0::/actions\n0::/actions/job"} {
		if _, err := cgroupDirectory(member, "29 1 0:27 /actions /limits rw - cgroup2 cgroup rw"); err == nil {
			t.Errorf("accepted unavailable/ambiguous/escaping member %q", member)
		}
	}
	if _, err := cgroupDirectory("0::/", "malformed"); err == nil {
		t.Fatal("invented a cgroup mount")
	}
}

func TestHostEvidenceRetainsInheritedLimitsAndUnavailableCounters(t *testing.T) {
	root := fstest.MapFS{}
	put := func(name, data string) { root[name] = &fstest.MapFile{Data: []byte(data)} }
	put("proc/self/cgroup", "0::/actions/job\n")
	put("proc/self/mountinfo", "29 1 0:27 / /sys/fs/cgroup rw - cgroup2 cgroup rw\n")
	put("proc/sys/kernel/random/boot_id", "fixture-boot\n")
	put("proc/123/comm", "fixture-worker\n")
	put("proc/self/task/123/schedstat", "100 200 3\n")
	put("proc/self/environ", "secret-must-not-appear")
	put("proc/123/cmdline", "secret-must-not-appear")
	for _, cg := range []struct{ dir, memory, cpu string }{
		{"sys/fs/cgroup/actions/job", "max", "max 100000"},
		{"sys/fs/cgroup/actions", "2000000000", "200000 100000"},
		{"sys/fs/cgroup", "max", "max 100000"},
	} {
		put(path.Join(cg.dir, "memory.max"), cg.memory)
		put(path.Join(cg.dir, "cpu.max"), cg.cpu)
	}
	h := captureHost(root)
	if h.Kind != "host-evidence-only" || len(h.Cgroups) != 3 || len(h.Processes) != 1 || len(h.Threads) != 1 {
		t.Fatalf("incomplete evidence: %+v", h)
	}
	if h.Cgroups[1].Files["sys/fs/cgroup/actions/memory.max"] != "2000000000" {
		t.Fatal("leaf max hid ancestor's finite limit")
	}
	if h.Missing["proc/stat"] == "" || h.Cgroups[0].Missing["sys/fs/cgroup/actions/job/memory.events"] == "" {
		t.Fatal("unavailable counters silently disappeared")
	}
	for key, value := range h.Files {
		if strings.Contains(value, "secret-must-not-appear") || strings.Contains(key, "environ") || strings.Contains(key, "cmdline") {
			t.Fatal("captured secrets or process arguments")
		}
	}
}

func TestOversizedTelemetryIsUnavailableNotTruncated(t *testing.T) {
	root := fstest.MapFS{"proc/stat": &fstest.MapFile{Data: []byte(strings.Repeat("x", maxTelemetryFile+1))}}
	h := captureHost(root)
	if _, ok := h.Files["proc/stat"]; ok || !strings.Contains(h.Missing["proc/stat"], "not silently truncated") {
		t.Fatalf("oversized telemetry became valid evidence: %v", h.Missing)
	}
}
