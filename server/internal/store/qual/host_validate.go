package qual

import (
	"encoding/hex"
	"fmt"
	"math"
	"math/big"
	"path"
	"strconv"
	"strings"
)

type hostValidator struct{ problems []string }

func (v *hostValidator) check(err error, name string) {
	if err != nil {
		v.problems = append(v.problems, name+": "+err.Error())
	}
}
func (v *hostValidator) require(ok bool, message string) {
	if !ok {
		v.problems = append(v.problems, message)
	}
}
func rawFile(files map[string]string, missing map[string]string, faults map[string]TelemetryError, name string) (string, error) {
	if e, ok := faults[name]; ok {
		return files[name], fmt.Errorf("%s: %s", e.Kind, e.Detail)
	}
	if e, ok := missing[name]; ok {
		return files[name], fmt.Errorf("unavailable: %s", e)
	}
	s, ok := files[name]
	if !ok {
		return "", fmt.Errorf("missing evidence")
	}
	return s, nil
}
func (v *hostValidator) file(h HostEvidence, name string) string {
	s, e := rawFile(h.Files, h.Missing, h.Errors, name)
	v.check(e, name)
	return s
}

// ValidateHost proves the declared unthrottled four-vCPU/16-GB-class VM
// stratum separately from process affinity and capped cgroup service. A subtree
// mount cannot prove limits above its visible root, so it cannot qualify here.
// Missing, unreadable or malformed required evidence never means unlimited.
func ValidateHost(h HostEvidence, profile string) []string {
	v := hostValidator{problems: []string{}}
	v.require(h.Version == 3 && h.Kind == "host-evidence-only", "unsupported host evidence version/kind")
	namespaceErr := proveRootNamespaces(h.Namespaces, h.NamespacesAfter) // MUTATION: namespace-proof
	v.check(namespaceErr, "namespace provenance")
	v.require(profile == "standard" || profile == "legacy8" || profile == "ram2gb", "unknown profile")
	v.require(h.GOOS == "linux" && h.GOARCH == "amd64", "expected Linux amd64 hosted stratum")
	v.require(h.CPUs == 4, "runtime usable CPU count must be four") // MUTATION: usable-cpus
	online, e := cpuList(v.file(h, "sys/devices/system/cpu/online"))
	v.check(e, "online CPUs")
	present, e := cpuList(v.file(h, "sys/devices/system/cpu/present"))
	v.check(e, "present CPUs")
	info, e := cpuInfo(v.file(h, "proc/cpuinfo"))
	v.check(e, "cpuinfo")
	v.require(len(online) == 4, "VM logical CPU count must be four, independently of affinity") // MUTATION: hardware-cpus
	v.require(equalSet(online, present) && equalSet(online, info), "online/present/cpuinfo CPU identities disagree")
	v.check(coherentTopology(h, online), "core/SMT topology") // MUTATION: coherent-topology
	mem := v.file(h, "proc/meminfo")
	memKiB, e := memInfo(mem)
	v.check(e, "meminfo")
	// The hosted profile is nominal 16 GiB, with OS-reserved pages reported below
	// that. A larger VM is a different hardware stratum, even if capped/affined.
	v.require(memKiB >= 15_625_000 && memKiB <= 16*1024*1024, "physical RAM must be 16 GB through 16 GiB")
	statCPUs, e := procStat(v.file(h, "proc/stat"))
	v.check(e, "proc/stat")
	v.require(equalSet(statCPUs, online), "proc/stat CPU identities disagree")
	v.check(loadAverage(v.file(h, "proc/loadavg")), "loadavg")
	v.check(diskStats(v.file(h, "proc/diskstats")), "diskstats")
	affinity, e := processStatus(v.file(h, "proc/self/status"))
	v.check(e, "process status")
	v.check(processStat(v.file(h, "proc/self/stat")), "process stat")
	kernel := strings.TrimSpace(v.file(h, "proc/sys/kernel/osrelease"))
	parts := strings.Split(kernel, ".")
	if len(parts) < 2 {
		v.require(false, "invalid kernel release")
	} else {
		_, e := unsigned(parts[0])
		v.check(e, "kernel major")
		_, e = unsigned(parts[1])
		v.check(e, "kernel minor")
	}
	boot := strings.TrimSpace(v.file(h, "proc/sys/kernel/random/boot_id"))
	v.require(validUUID(boot), "invalid boot UUID")
	for _, name := range []string{"cpu", "io", "memory"} {
		v.check(pressure(v.file(h, "proc/pressure/"+name), name != "cpu"), "proc pressure "+name)
	}
	v.check(processEvidence(h, "proc", h.ProcessPaths, h.Processes, false), "process enumeration")
	v.check(processEvidence(h, "proc/self/task", h.ThreadPaths, h.Threads, true), "thread enumeration")
	loc, e := cgroupDirectory(v.file(h, "proc/self/cgroup"), v.file(h, "proc/self/mountinfo"))
	v.check(e, "cgroup location")
	v.require(h.MountRoot == loc.root && h.MountPoint == loc.mount, "cgroup mount identity not retained")
	v.require(loc.root == "/", "subtree mount hides ancestor limits") // MUTATION: subtree-visibility
	v.require(len(h.Cgroups) > 0, "missing cgroup hierarchy")
	expected := loc.directory
	minimumMemory, minimumSwap := uint64(math.MaxUint64), uint64(math.MaxUint64)
	var minimumCPU *big.Rat
	effective := online
	for index, cg := range h.Cgroups {
		v.require(cg.Path == expected, "cgroup ancestor omitted/reordered")
		if expected != loc.mount {
			expected = path.Dir(expected)
		} else {
			v.require(index == len(h.Cgroups)-1, "extra cgroup ancestor")
		}
		realRoot := namespaceErr == nil && cg.Path == loc.mount && loc.root == "/"
		read := func(name string, rootMayLack bool) (string, bool) {
			key := path.Join(cg.Path, name)
			_, present := cg.Files[key]
			// These control files do not exist on the genuine v2 hierarchy root.
			// Readable "max" is not proof: a namespace remount can expose it.
			rootControl := name == "cpu.max" || name == "memory.max" || name == "memory.swap.max" || name == "io.max"
			v.require(!(realRoot && rootControl && present), "unexpected root control file: "+key) // MUTATION: root-control
			if fault, ok := cg.Errors[key]; ok && fault.Kind == "not-exist" && fault.Operation == "open" && !present && realRoot && rootMayLack {
				return "", true
			} // MUTATION: root-absence
			s, err := rawFile(cg.Files, cg.Missing, cg.Errors, key)
			v.check(err, key)
			return s, false
		}
		controllers, _ := read("cgroup.controllers", false)
		cs, e := wordSet(controllers)
		v.check(e, "controllers")
		v.require(cs["cpu"] && cs["cpuset"] && cs["memory"] && cs["io"], "required CPU/cpuset/memory/IO controller unavailable")
		for _, name := range []string{"memory.max", "memory.swap.max"} {
			s, absent := read(name, true)
			if absent {
				continue
			}
			n, e := limit(s)
			v.check(e, cg.Path+"/"+name)
			if name == "memory.max" {
				minimumMemory = min(minimumMemory, n)
			} else {
				minimumSwap = min(minimumSwap, n)
			}
		}
		s, absent := read("cpu.max", true)
		if !absent {
			q, e := cpuQuota(s)
			v.check(e, cg.Path+"/cpu.max")
			if q != nil && (minimumCPU == nil || q.Cmp(minimumCPU) < 0) {
				minimumCPU = q
			}
		}
		s, _ = read("cpuset.cpus.effective", false)
		set, e := cpuList(s)
		v.check(e, cg.Path+"/cpuset")
		v.require(subset(set, online), "cgroup cpuset includes unknown CPU")
		effective = intersect(effective, set)
		s, absent = read("io.max", true)
		if !absent {
			finite, e := ioMax(s)
			v.check(e, cg.Path+"/io.max")
			v.require(!finite, "undeclared finite IO throttle at "+cg.Path)
		} // MUTATION: io-throttle
		s, _ = read("io.stat", false)
		v.check(ioStat(s), cg.Path+"/io.stat")
		s, _ = read("io.pressure", false)
		v.check(pressure(s, true), cg.Path+"/io.pressure")
		s, _ = read("cpu.pressure", false)
		v.check(pressure(s, false), cg.Path+"/cpu.pressure")
		s, _ = read("memory.pressure", false)
		v.check(pressure(s, true), cg.Path+"/memory.pressure")
		for _, name := range []string{"memory.current", "memory.peak", "memory.swap.current"} {
			s, absent := read(name, true)
			if !absent {
				_, e := unsigned(strings.TrimSpace(s))
				v.check(e, cg.Path+"/"+name)
			}
		}
		for name, required := range map[string][]string{"memory.stat": {"anon", "file", "pgfault", "pgmajfault"}, "memory.events": {"low", "high", "max", "oom", "oom_kill"}, "cpu.stat": {"usage_usec", "user_usec", "system_usec"}} {
			s, absent := read(name, name == "memory.events")
			if !absent {
				v.check(counters(s, required), cg.Path+"/"+name)
			} // MUTATION: numeric-counters
		}
	}
	if len(h.Cgroups) > 0 {
		v.require(h.Cgroups[len(h.Cgroups)-1].Path == loc.mount, "incomplete ancestor chain")
	}
	v.require(len(affinity) == 4 && equalSet(affinity, effective), "process affinity and effective cgroup cpuset must agree on four CPUs") // MUTATION: affinity
	if profile == "standard" {
		v.require(minimumMemory >= 16_000_000_000 && (minimumCPU == nil || minimumCPU.Cmp(big.NewRat(4, 1)) >= 0), "standard inherits insufficient memory or CPU quota")
	} else {
		want := uint64(8 << 30)
		if profile == "ram2gb" {
			want = 2_000_000_000
		}
		v.require(minimumMemory == want && minimumSwap == 0 && minimumCPU != nil && minimumCPU.Cmp(big.NewRat(2, 1)) == 0, "effective memory/swap/CPU quota does not match capped profile") // MUTATION: capped-limits
	}
	return v.problems
}

func unsigned(s string) (uint64, error) {
	if s == "" || strings.IndexFunc(s, func(r rune) bool { return r < '0' || r > '9' }) >= 0 {
		return 0, fmt.Errorf("expected unsigned decimal")
	}
	return strconv.ParseUint(s, 10, 64)
}
func uintFields(s string, n int) error {
	f := strings.Fields(s)
	if len(f) != n {
		return fmt.Errorf("expected %d integer fields", n)
	}
	for _, x := range f {
		if _, e := unsigned(x); e != nil {
			return e
		}
	}
	return nil
}
func limit(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	if s == "max" {
		return math.MaxUint64, nil
	}
	return unsigned(s)
}
func cpuQuota(s string) (*big.Rat, error) {
	f := strings.Fields(s)
	if len(f) != 2 {
		return nil, fmt.Errorf("expected quota and period")
	}
	period, e := unsigned(f[1])
	if e != nil || period == 0 {
		return nil, fmt.Errorf("invalid CPU period")
	}
	if f[0] == "max" {
		return nil, nil
	}
	q, e := unsigned(f[0])
	if e != nil || q == 0 {
		return nil, fmt.Errorf("invalid CPU quota")
	}
	return new(big.Rat).SetFrac(new(big.Int).SetUint64(q), new(big.Int).SetUint64(period)), nil
}
func cpuList(s string) (map[uint64]bool, error) {
	r := map[uint64]bool{}
	s = strings.TrimSpace(s)
	if s == "" {
		return r, fmt.Errorf("empty CPU list")
	}
	for _, part := range strings.Split(s, ",") {
		f := strings.Split(part, "-")
		if len(f) > 2 {
			return r, fmt.Errorf("bad CPU range")
		}
		lo, e := unsigned(f[0])
		if e != nil || lo > 65535 {
			return r, fmt.Errorf("bad CPU id")
		}
		hi := lo
		if len(f) == 2 {
			hi, e = unsigned(f[1])
			if e != nil || hi < lo || hi > 65535 {
				return r, fmt.Errorf("bad CPU range")
			}
		}
		for id := lo; id <= hi; id++ {
			if r[id] {
				return r, fmt.Errorf("duplicate CPU")
			}
			r[id] = true
		}
	}
	return r, nil
}
func equalSet(a, b map[uint64]bool) bool { return len(a) == len(b) && subset(a, b) }
func subset(a, b map[uint64]bool) bool {
	for id := range a {
		if !b[id] {
			return false
		}
	}
	return true
}
func intersect(a, b map[uint64]bool) map[uint64]bool {
	r := map[uint64]bool{}
	for id := range a {
		if b[id] {
			r[id] = true
		}
	}
	return r
}
func wordSet(s string) (map[string]bool, error) {
	r := map[string]bool{}
	for _, f := range strings.Fields(s) {
		if r[f] {
			return r, fmt.Errorf("duplicate field")
		}
		r[f] = true
	}
	if len(r) == 0 {
		return r, fmt.Errorf("empty fields")
	}
	return r, nil
}
func cpuInfo(s string) (map[uint64]bool, error) {
	r := map[uint64]bool{}
	for _, block := range strings.Split(strings.TrimSpace(s), "\n\n") {
		m := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			f := strings.SplitN(line, ":", 2)
			if len(f) != 2 {
				return r, fmt.Errorf("malformed cpuinfo")
			}
			key := strings.TrimSpace(f[0])
			if _, ok := m[key]; ok {
				return r, fmt.Errorf("duplicate cpuinfo field")
			}
			m[key] = strings.TrimSpace(f[1])
		}
		id, e := unsigned(m["processor"])
		if e != nil || r[id] || m["model name"] == "" {
			return r, fmt.Errorf("invalid processor identity/model")
		}
		r[id] = true
	}
	return r, nil
}
func counters(s string, required []string) error {
	m := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 || m[f[0]] {
			return fmt.Errorf("malformed or duplicate counter")
		}
		if _, e := unsigned(f[1]); e != nil {
			return e
		}
		m[f[0]] = true
	}
	for _, key := range required {
		if !m[key] {
			return fmt.Errorf("missing %s counter", key)
		}
	}
	return nil
}
func finiteNumber(s string) bool {
	n, e := strconv.ParseFloat(s, 64)
	return e == nil && !math.IsNaN(n) && !math.IsInf(n, 0) && n >= 0
}
func pressure(s string, full bool) error {
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		f := strings.Fields(line)
		if len(f) != 5 || (f[0] != "some" && f[0] != "full") || seen[f[0]] {
			return fmt.Errorf("bad pressure line")
		}
		seen[f[0]] = true
		for i, key := range []string{"avg10", "avg60", "avg300", "total"} {
			p := strings.SplitN(f[i+1], "=", 2)
			if len(p) != 2 || p[0] != key {
				return fmt.Errorf("bad pressure field")
			}
			if key == "total" {
				if _, e := unsigned(p[1]); e != nil {
					return e
				}
			} else {
				n, e := strconv.ParseFloat(p[1], 64)
				if !finiteNumber(p[1]) || e != nil || n > 100 {
					return fmt.Errorf("bad pressure average")
				}
			}
		}
	}
	if !seen["some"] || (full && !seen["full"]) {
		return fmt.Errorf("missing pressure population")
	}
	return nil
}
func device(s string) bool {
	f := strings.Split(s, ":")
	if len(f) != 2 {
		return false
	}
	_, a := unsigned(f[0])
	_, b := unsigned(f[1])
	return a == nil && b == nil
}
func ioMax(s string) (bool, error) {
	finite := false
	seen := map[string]bool{}
	s = strings.TrimSpace(s)
	if s == "" {
		return false, nil
	}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || !device(f[0]) || seen[f[0]] {
			return finite, fmt.Errorf("bad IO device limit")
		}
		seen[f[0]] = true
		keys := map[string]bool{}
		for _, field := range f[1:] {
			p := strings.SplitN(field, "=", 2)
			if len(p) != 2 || keys[p[0]] || !(p[0] == "rbps" || p[0] == "wbps" || p[0] == "riops" || p[0] == "wiops") {
				return finite, fmt.Errorf("bad IO limit field")
			}
			keys[p[0]] = true
			if p[1] != "max" {
				if _, e := unsigned(p[1]); e != nil {
					return finite, e
				}
				finite = true
			}
		}
		if len(keys) != 4 { // MUTATION: io-completeness
			return finite, fmt.Errorf("incomplete IO limit record; need rbps/wbps/riops/wiops")
		}
	}
	return finite, nil
}
func ioStat(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) == 0 || !device(f[0]) || seen[f[0]] {
			return fmt.Errorf("bad IO stat device")
		}
		seen[f[0]] = true
		// Linux blkcg_print_one_stat prints the device before conditionally
		// emitting the core read/write fields. A bare device is a valid
		// no-read/write record, not an incomplete populated record. Keep raw
		// text; do not synthesize zero discard/delay/policy-specific counters.
		if len(f) == 1 {
			continue
		}
		fields := map[string]bool{}
		for _, field := range f[1:] {
			p := strings.SplitN(field, "=", 2)
			if len(p) != 2 || fields[p[0]] {
				return fmt.Errorf("bad IO stat field")
			}
			if _, e := unsigned(p[1]); e != nil {
				return e
			}
			fields[p[0]] = true
		}
		for _, key := range []string{"rbytes", "wbytes", "rios", "wios"} {
			if !fields[key] {
				return fmt.Errorf("missing IO stat %s", key)
			}
		}
	}
	return nil
}
func memInfo(s string) (uint64, error) {
	var total uint64
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || len(f) > 3 || !strings.HasSuffix(f[0], ":") || seen[f[0]] {
			return 0, fmt.Errorf("bad meminfo line")
		}
		seen[f[0]] = true
		n, e := unsigned(f[1])
		if e != nil {
			return 0, e
		}
		if len(f) == 3 && f[2] != "kB" {
			return 0, fmt.Errorf("bad meminfo unit")
		}
		if f[0] == "MemTotal:" {
			if len(f) != 3 {
				return 0, fmt.Errorf("missing MemTotal unit")
			}
			total = n
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("missing MemTotal")
	}
	return total, nil
}
func procStat(s string) (map[uint64]bool, error) {
	ids := map[uint64]bool{}
	aggregate := false
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			return ids, fmt.Errorf("bad proc stat line")
		}
		if seen[f[0]] {
			return ids, fmt.Errorf("duplicate proc stat key")
		}
		seen[f[0]] = true
		for _, x := range f[1:] {
			if _, e := unsigned(x); e != nil {
				return ids, e
			}
		}
		if strings.HasPrefix(f[0], "cpu") {
			if len(f) < 9 {
				return ids, fmt.Errorf("incomplete CPU counters")
			}
			if f[0] == "cpu" {
				if aggregate {
					return ids, fmt.Errorf("duplicate CPU aggregate")
				}
				aggregate = true
			} else {
				id, e := unsigned(strings.TrimPrefix(f[0], "cpu"))
				if e != nil || ids[id] {
					return ids, fmt.Errorf("bad CPU counter id")
				}
				ids[id] = true
			}
		}
	}
	for _, key := range []string{"ctxt", "btime", "processes", "procs_running", "procs_blocked"} {
		if !seen[key] {
			return ids, fmt.Errorf("missing proc stat %s", key)
		}
	}
	if !aggregate || len(ids) == 0 {
		return ids, fmt.Errorf("missing CPU counters")
	}
	return ids, nil
}
func loadAverage(s string) error {
	f := strings.Fields(s)
	if len(f) != 5 {
		return fmt.Errorf("bad loadavg")
	}
	for _, x := range f[:3] {
		if !finiteNumber(x) {
			return fmt.Errorf("invalid load")
		}
	}
	p := strings.Split(f[3], "/")
	if len(p) != 2 {
		return fmt.Errorf("bad runnable count")
	}
	a, e := unsigned(p[0])
	b, err := unsigned(p[1])
	if e != nil || err != nil || a > b || b == 0 {
		return fmt.Errorf("invalid runnable count")
	}
	_, e = unsigned(f[4])
	return e
}
func diskStats(s string) error {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for _, line := range lines {
		f := strings.Fields(line)
		if len(f) < 14 {
			return fmt.Errorf("short diskstats")
		}
		for i, x := range f {
			if i == 2 {
				continue
			}
			if _, e := unsigned(x); e != nil {
				return e
			}
		}
	}
	return nil
}
func processStatus(s string) (map[uint64]bool, error) {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(s), "\n") {
		p := strings.SplitN(line, ":", 2)
		if len(p) != 2 {
			return nil, fmt.Errorf("bad process status")
		}
		if _, ok := m[p[0]]; ok {
			return nil, fmt.Errorf("duplicate process status")
		}
		m[p[0]] = strings.TrimSpace(p[1])
	}
	for _, key := range []string{"VmRSS", "VmHWM", "RssAnon", "RssFile", "VmSwap"} {
		f := strings.Fields(m[key])
		if len(f) != 2 || f[1] != "kB" {
			return nil, fmt.Errorf("bad %s", key)
		}
		if _, e := unsigned(f[0]); e != nil {
			return nil, e
		}
	}
	n, e := unsigned(m["Threads"])
	if e != nil || n == 0 {
		return nil, fmt.Errorf("invalid Threads")
	}
	return cpuList(m["Cpus_allowed_list"])
}
func processStat(s string) error {
	end := strings.LastIndex(s, ")")
	start := strings.Index(s, " (")
	if end < start || start < 1 {
		return fmt.Errorf("bad process stat")
	}
	pid, e := unsigned(s[:start])
	if e != nil || pid == 0 {
		return fmt.Errorf("bad process PID")
	}
	f := strings.Fields(s[end+1:])
	if len(f) < 50 || len(f[0]) != 1 || !strings.Contains("RSDTtXZxKWPI", f[0]) {
		return fmt.Errorf("short process stat")
	}
	for _, x := range f[1:] {
		if _, e := strconv.ParseUint(x, 10, 64); e != nil {
			if _, e = strconv.ParseInt(x, 10, 64); e != nil {
				return fmt.Errorf("bad process stat number")
			}
		}
	}
	return nil
}
func validUUID(s string) bool {
	f := strings.Split(s, "-")
	if len(f) != 5 {
		return false
	}
	for i, n := range []int{8, 4, 4, 4, 12} {
		if len(f[i]) != n {
			return false
		}
	}
	b, e := hex.DecodeString(strings.Join(f, ""))
	return e == nil && len(b) == 16
}
