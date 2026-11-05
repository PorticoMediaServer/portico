package qual

import (
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
	"testing"
)

func validHost(t *testing.T) HostEvidence {
	t.Helper()
	b, e := os.ReadFile("testdata/host-valid.json")
	if e != nil {
		t.Fatal(e)
	}
	var h HostEvidence
	if e = json.Unmarshal(b, &h); e != nil {
		t.Fatal(e)
	}
	return h
}
func setCG(h *HostEvidence, index int, name, value string) {
	cg := &h.Cgroups[index]
	key := path.Join(cg.Path, name)
	cg.Files[key] = value
	delete(cg.Missing, key)
	delete(cg.Errors, key)
}
func missingCG(h *HostEvidence, index int, name, kind string) {
	cg := &h.Cgroups[index]
	key := path.Join(cg.Path, name)
	delete(cg.Files, key)
	cg.Missing[key] = kind
	cg.Errors[key] = TelemetryError{Kind: kind, Operation: "open", Detail: kind}
}
func rootPresent(h *HostEvidence) {
	for name, value := range map[string]string{"cpu.max": "max 100000", "memory.max": "max", "memory.swap.max": "max", "memory.current": "2000", "memory.peak": "3000", "memory.swap.current": "0", "memory.events": "low 0\nhigh 0\nmax 0\noom 0\noom_kill 0", "io.max": ""} {
		setCG(h, 2, name, value)
	}
}
func rejectHost(t *testing.T, h HostEvidence) {
	t.Helper()
	if p := ValidateHost(h, "legacy8"); len(p) == 0 {
		t.Fatal("invalid host qualified")
	}
}

func TestHostValidProfilesAndNonzeroCounters(t *testing.T) {
	for _, profile := range []string{"standard", "legacy8", "ram2gb"} {
		h := validHost(t)
		if profile == "standard" {
			setCG(&h, 0, "cpu.max", "max 100000")
			setCG(&h, 0, "memory.max", "max")
		} else if profile == "ram2gb" {
			setCG(&h, 0, "memory.max", "2000000000")
		}
		if p := ValidateHost(h, profile); len(p) > 0 {
			t.Fatalf("%s: %v", profile, p)
		}
	}
	h := validHost(t)
	before := h.Cgroups[0].Files[path.Join(h.Cgroups[0].Path, "memory.events")]
	if len(ValidateHost(h, "legacy8")) != 0 || h.Cgroups[0].Files[path.Join(h.Cgroups[0].Path, "memory.events")] != before {
		t.Fatal("nonzero counters hidden or rejected")
	}
	// Readable empty io.max/stat means no limits/no IO. It differs from absent.
	setCG(&h, 0, "io.stat", "")
	setCG(&h, 1, "io.max", "8:0 rbps=max wbps=max riops=max wiops=max")
	if p := ValidateHost(h, "legacy8"); len(p) > 0 {
		t.Fatal(p)
	}
}
func TestHostUsableCPUGuard(t *testing.T) { h := validHost(t); h.CPUs = 12; rejectHost(t, h) }
func TestHostMaskedTwelveCPUs(t *testing.T) {
	h := validHost(t)
	h.Files["sys/devices/system/cpu/online"] = "0-11"
	h.Files["sys/devices/system/cpu/present"] = "0-11"
	info, stat := "", "cpu 100 0 50 1000 10 0 5 3 0 0\n"
	for i := 0; i < 12; i++ {
		info += fmt.Sprintf("processor : %d\nmodel name : Fixture VM CPU\n\n", i)
		stat += fmt.Sprintf("cpu%d 1 2 3 4 5 6 7 8 9 10\n", i)
		base := fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology/", i)
		h.Topology[base+"core_id"] = fmt.Sprint(i / 2)
		h.Topology[base+"physical_package_id"] = "0"
		h.Topology[base+"thread_siblings_list"] = fmt.Sprintf("%d-%d", i/2*2, i/2*2+1)
	}
	h.Files["proc/cpuinfo"] = info
	h.Files["proc/stat"] = stat + "ctxt 1000\nbtime 1\nprocesses 100\nprocs_running 1\nprocs_blocked 0\n"
	setCG(&h, 2, "cpuset.cpus.effective", "0-11")
	// runtime/affinity and leaf cpuset are still 0-3; only hardware-count proof
	// distinguishes this masked larger VM. Physical RAM remains the valid16GB
	// class to isolate the CPU guard instead of failing incidentally on RAM.
	rejectHost(t, h)
}
func TestHostAffinityGuard(t *testing.T) {
	h := validHost(t)
	h.Files["proc/self/status"] = strings.Replace(h.Files["proc/self/status"], "Cpus_allowed_list: 0-3", "Cpus_allowed_list: 0-2", 1)
	rejectHost(t, h)
}
func TestHostHardwareRAMAndTopology(t *testing.T) {
	for _, change := range []func(*HostEvidence){func(h *HostEvidence) { h.Files["proc/meminfo"] = "MemTotal: 32768000 kB" }, func(h *HostEvidence) { delete(h.Topology, "sys/devices/system/cpu/cpu0/topology/core_id") }, func(h *HostEvidence) { h.Topology["sys/devices/system/cpu/cpu0/topology/thread_siblings_list"] = "2-3" }} {
		h := validHost(t)
		change(&h)
		rejectHost(t, h)
	}
}
func TestHostSubtreeVisibilityGuard(t *testing.T) {
	h := validHost(t)
	rootPresent(&h)
	h.MountRoot = "/tenant"
	h.Files["proc/self/mountinfo"] = strings.Replace(h.Files["proc/self/mountinfo"], " / /sys/", " /tenant /sys/", 1)
	h.Files["proc/self/cgroup"] = "0::/tenant/jobs/service\n"
	rejectHost(t, h)
}
func TestHostRootAbsenceGuard(t *testing.T) {
	for _, name := range []string{"cpu.max", "memory.max", "memory.swap.max", "io.max"} {
		for _, kind := range []string{"permission", "open"} {
			t.Run(name+kind, func(t *testing.T) { h := validHost(t); missingCG(&h, 2, name, kind); rejectHost(t, h) })
		}
	}
}
func TestHostMissingAncestorLimits(t *testing.T) {
	for i := 0; i < 3; i++ {
		for _, name := range []string{"cpu.max", "memory.max", "memory.swap.max"} {
			h := validHost(t)
			missingCG(&h, i, name, "permission")
			rejectHost(t, h)
		}
	}
	h := validHost(t)
	h.Cgroups = h.Cgroups[:1]
	rejectHost(t, h)
}
func TestHostIOThrottleGuard(t *testing.T) {
	for i := 0; i < 3; i++ {
		h := validHost(t)
		setCG(&h, i, "io.max", "8:0 rbps=1 wbps=1 riops=1 wiops=1")
		rejectHost(t, h)
	}
}
func TestHostIOEvidenceRequired(t *testing.T) {
	for i := 0; i < 3; i++ {
		for _, name := range []string{"io.max", "io.stat", "io.pressure", "cgroup.controllers"} {
			for _, kind := range []string{"permission", "not-exist"} {
				if i == 2 && name == "io.max" && kind == "not-exist" {
					continue
				}
				h := validHost(t)
				missingCG(&h, i, name, kind)
				rejectHost(t, h)
			}
		}
	}
}
func TestHostMalformedRequiredEvidence(t *testing.T) {
	for name, bad := range map[string]string{"memory.current": "not-a-number", "memory.events": "", "memory.stat": "anon x\nfile 4\npgfault 1\npgmajfault 0", "cpu.stat": "usage_usec -1\nuser_usec 1\nsystem_usec 1", "cpuset.cpus.effective": "not-a-cpu-list", "cpu.max": "max 0", "memory.max": "NaN", "memory.swap.max": "-1", "io.max": "8:0 rbps=wat", "io.stat": "8:0 rbytes=NaN wbytes=0 rios=0 wios=0", "io.pressure": "some avg10=NaN avg60=0 avg300=0 total=0", "cgroup.controllers": "cpu memory cpuset"} {
		t.Run(name, func(t *testing.T) { h := validHost(t); setCG(&h, 0, name, bad); rejectHost(t, h) })
	}
	for name, bad := range map[string]string{"proc/loadavg": "fixture", "proc/diskstats": "", "proc/stat": "cpu fixture", "proc/self/stat": "123 (host) R NaN", "proc/self/status": "Cpus_allowed_list: wat", "proc/meminfo": "MemTotal: nope kB", "proc/pressure/io": "fixture", "proc/sys/kernel/random/boot_id": "not-uuid"} {
		t.Run(name, func(t *testing.T) { h := validHost(t); h.Files[name] = bad; rejectHost(t, h) })
	}
	h := validHost(t)
	for key := range h.Threads {
		h.Threads[key] = "100 invalid 3"
	}
	rejectHost(t, h)
}
func TestHostNumericCounterGuard(t *testing.T) {
	h := validHost(t)
	setCG(&h, 0, "memory.events", "")
	rejectHost(t, h)
}
func TestHostCappedLimitsGuard(t *testing.T) {
	for _, change := range []struct{ name, value string }{{"memory.max", "8000000000"}, {"memory.swap.max", "max"}, {"cpu.max", "100000 100000"}} {
		h := validHost(t)
		setCG(&h, 0, change.name, change.value)
		rejectHost(t, h)
	}
	h := validHost(t)
	setCG(&h, 1, "memory.max", "1000000000")
	rejectHost(t, h)
}

func TestHostIOMissingReadGuard(t *testing.T) {
	h := validHost(t)
	missingCG(&h, 1, "io.max", "permission")
	rejectHost(t, h)
}
func TestHostRootNonexistReadAndConflict(t *testing.T) {
	h := validHost(t)
	key := path.Join(h.Cgroups[2].Path, "memory.max")
	e := h.Cgroups[2].Errors[key]
	e.Operation = "read"
	h.Cgroups[2].Errors[key] = e
	rejectHost(t, h)
	h = validHost(t)
	h.Cgroups[2].Files[key] = ""
	rejectHost(t, h)
}
