package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/qual"
)

func evidence() qual.HostEvidence {
	h := qual.HostEvidence{GOOS: "linux", CPUs: 4, Files: map[string]string{}, Processes: map[string]string{"1": "fixture"}, Threads: map[string]string{"1": "0 0 0"}}
	for _, name := range []string{"cpuinfo", "meminfo", "stat", "loadavg", "diskstats", "sys/kernel/osrelease", "sys/kernel/random/boot_id", "self/status", "self/mountinfo", "pressure/cpu", "pressure/io", "pressure/memory"} {
		h.Files["proc/"+name] = "fixture"
	}
	h.Files["proc/meminfo"] = "MemTotal: 16384000 kB\n"
	h.Cgroups = []qual.CgroupEvidence{{Path: "limits/job", Files: map[string]string{}}}
	for _, name := range []string{"memory.current", "memory.stat", "memory.events", "cpu.stat", "cpuset.cpus.effective"} {
		h.Cgroups[0].Files["limits/job/"+name] = "fixture"
	}
	h.Cgroups[0].Files["limits/job/memory.max"] = "8589934592"
	h.Cgroups[0].Files["limits/job/memory.swap.max"] = "0"
	h.Cgroups[0].Files["limits/job/cpu.max"] = "200000 100000"
	return h
}

func TestActualProfileLimits(t *testing.T) {
	for _, tt := range []struct {
		profile, memory, swap, cpu string
		valid                      bool
	}{
		{"legacy8", "8589934592", "0", "200000 100000", true},
		{"legacy8", "8000000000", "0", "200000 100000", false},
		{"legacy8", "8589934592", "max", "200000 100000", false},
		{"legacy8", "8589934592", "0", "100000 100000", false},
		{"legacy8", "8589934592", "0", "garbled", false},
		{"ram2gb", "2000000000", "0", "200000 100000", true},
		{"ram2gb", "2147483648", "0", "200000 100000", false},
		{"standard", "max", "max", "max 100000", true},
		{"standard", "8589934592", "0", "max 100000", false},
	} {
		h := evidence()
		h.Cgroups[0].Files["limits/job/memory.max"] = tt.memory
		h.Cgroups[0].Files["limits/job/memory.swap.max"] = tt.swap
		h.Cgroups[0].Files["limits/job/cpu.max"] = tt.cpu
		if got := validate(h, tt.profile); (len(got) == 0) != tt.valid {
			t.Errorf("profile %+v: %v", tt, got)
		}
	}
	h := evidence()
	h.Cgroups = append(h.Cgroups, qual.CgroupEvidence{Path: "limits", Files: map[string]string{}})
	for k, v := range h.Cgroups[0].Files {
		h.Cgroups[1].Files[strings.Replace(k, "limits/job/", "limits/", 1)] = v
	}
	h.Cgroups[1].Files["limits/memory.max"] = "1000000000"
	if len(validate(h, "legacy8")) == 0 {
		t.Fatal("ignored stricter ancestor limit")
	}
	// Linux's hierarchy root lacks memory.max/cpu.max/memory.current,
	// while an undelegated leaf can inherit its visible ancestor cpuset.
	h = evidence()
	delete(h.Cgroups[0].Files, "limits/job/cpuset.cpus.effective")
	h.Cgroups = append(h.Cgroups, qual.CgroupEvidence{Path: "limits", Files: map[string]string{"limits/cpuset.cpus.effective": "0-3"}})
	if problems := validate(h, "legacy8"); len(problems) != 0 {
		t.Fatalf("lost inherited affinity or demanded root-only-absent counters: %v", problems)
	}
}

func TestInvalidReceiptStillContainsActualEvidence(t *testing.T) {
	var out bytes.Buffer
	err := run([]string{"-source", strings.Repeat("a", 40), "-archive-sha256", strings.Repeat("b", 64), "-profile", "ram2gb", "-replicate", "1"}, &out, evidence)
	if err == nil {
		t.Fatal("8GiB actual limit passed 2GB label")
	}
	var receipt struct {
		Kind     string
		Host     qual.HostEvidence
		Problems []string `json:"protocol_errors"`
	}
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Kind != "host-protocol-check-only" || len(receipt.Problems) == 0 || receipt.Host.Cgroups[0].Files["limits/job/memory.max"] != "8589934592" {
		t.Fatalf("lost failing receipt: %s", out.Bytes())
	}
}
