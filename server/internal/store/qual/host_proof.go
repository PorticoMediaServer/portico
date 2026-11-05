package qual

import (
	"fmt"
	"path"
	"strings"
)

type cpuTopology struct {
	core, socket uint64
	siblings     map[uint64]bool
}

// Every online core group must name exactly the same reciprocal sibling set.
// Cross-check independent cpuinfo package/core claims where the kernel exposes
// them. Missing sysfs data, parse failures and contradictory sources are fatal.
func coherentTopology(h HostEvidence, online map[uint64]bool) error {
	cores := map[uint64]cpuTopology{}
	for id := range online {
		base := fmt.Sprintf("sys/devices/system/cpu/cpu%d/topology/", id)
		var c cpuTopology
		for name, into := range map[string]*uint64{"core_id": &c.core, "physical_package_id": &c.socket} {
			s, e := rawFile(h.Topology, h.Missing, h.Errors, base+name)
			if e != nil {
				return fmt.Errorf("%s: %w", base+name, e)
			}
			n, e := unsigned(strings.TrimSpace(s))
			if e != nil {
				return fmt.Errorf("%s: %w", base+name, e)
			}
			*into = n
		}
		s, e := rawFile(h.Topology, h.Missing, h.Errors, base+"thread_siblings_list")
		if e != nil {
			return e
		}
		c.siblings, e = cpuList(s)
		if e != nil {
			return e
		}
		cores[id] = c
	}
	for id, c := range cores {
		want := map[uint64]bool{}
		for other, d := range cores {
			if c.socket == d.socket && c.core == d.core {
				want[other] = true
			}
		}
		if !equalSet(c.siblings, want) {
			return fmt.Errorf("CPU%d nonreciprocal/contradictory core/SMT group", id)
		}
	}
	for _, block := range strings.Split(strings.TrimSpace(h.Files["proc/cpuinfo"]), "\n\n") {
		m := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			f := strings.SplitN(line, ":", 2)
			if len(f) == 2 {
				m[strings.TrimSpace(f[0])] = strings.TrimSpace(f[1])
			}
		}
		// cpuInfo separately validates all IDs/fields and hardware agreement.
		id, _ := unsigned(m["processor"])
		c := cores[id]
		for name, want := range map[string]uint64{"physical id": c.socket, "core id": c.core} {
			if s, ok := m[name]; ok {
				n, e := unsigned(s)
				if e != nil || n != want {
					return fmt.Errorf("CPU%d cpuinfo %s contradicts sysfs", id, name)
				}
			}
		}
	}
	return nil
}

// Require both the enumeration and every enumerated read to succeed. Checking
// only maps of successfully read bytes loses failed entries and treats partial
// reads/close failures as valid. Even an exited/vanished process invalidates
// this conservative snapshot; the caller can capture a fresh complete one.
func processEvidence(h HostEvidence, directory string, enumerated []string, values map[string]string, thread bool) error {
	if _, bad := h.Errors[directory]; bad { // MUTATION: enumeration-errors
		return fmt.Errorf("unavailable enumeration: %s", directory)
	}
	if _, bad := h.Missing[directory]; bad {
		return fmt.Errorf("missing enumeration: %s", directory)
	}
	if len(enumerated) == 0 {
		return fmt.Errorf("no enumerated entries: %s", directory)
	}
	paths := map[string]bool{}
	for _, name := range enumerated {
		if paths[name] {
			return fmt.Errorf("duplicate enumerated path: %s", name)
		}
		paths[name] = true
	}
	// An explicitly failed entry is not allowed to disappear from the expected
	// list, nor can a readable neighbour compensate for it.
	for name := range values {
		paths[name] = true
	}
	for name := range h.Errors {
		if strings.HasPrefix(name, directory+"/") && strings.Count(strings.TrimPrefix(name, directory+"/"), "/") == 1 {
			paths[name] = true
		}
	}
	for name := range h.Missing {
		if strings.HasPrefix(name, directory+"/") && strings.Count(strings.TrimPrefix(name, directory+"/"), "/") == 1 {
			paths[name] = true
		}
	}
	for name := range paths {
		f := strings.Split(strings.TrimPrefix(name, directory+"/"), "/")
		want := "comm"
		if thread {
			want = "schedstat"
		}
		if len(f) != 2 || f[1] != want || path.Join(directory, f[0], want) != name {
			return fmt.Errorf("invalid enumerated path: %s", name)
		}
		if _, e := unsigned(f[0]); e != nil {
			return e
		}
		s, e := rawFile(values, h.Missing, h.Errors, name)
		if e != nil { // MUTATION: process-read-errors
			return fmt.Errorf("%s: %w", name, e)
		}
		if thread {
			if e := uintFields(s, 3); e != nil { // MUTATION: thread-numbers
				return fmt.Errorf("%s: %w", name, e)
			}
		} else if strings.TrimSpace(s) == "" || strings.ContainsRune(s, '\x00') {
			return fmt.Errorf("invalid process name: %s", name)
		}
	}
	return nil
}
