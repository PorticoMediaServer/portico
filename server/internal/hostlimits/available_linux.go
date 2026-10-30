//go:build linux

package hostlimits

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AvailableMemory reports host availability clamped by every visible cgroup
// ancestor's remaining allowance. tmpfs pages are charged to these allowances.
// Missing usage under a finite limit is unknown, never unlimited.
func AvailableMemory() (available, total int64, err error) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, err
	}
	availableKnown := false
	for _, line := range strings.Split(string(raw), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 || f[2] != "kB" {
			continue
		}
		n, e := strconv.ParseInt(f[1], 10, 64)
		if e != nil || n < 0 || n > (1<<63-1)/1024 {
			continue
		}
		switch f[0] {
		case "MemAvailable:":
			available = n * 1024
			availableKnown = true
		case "MemTotal:":
			total = n * 1024
		}
	}
	if total <= 0 || available < 0 || available > total {
		return 0, 0, errors.New("unreadable host memory")
	}
	// A missing MemAvailable must also fail closed.
	if !availableKnown {
		return 0, 0, errors.New("missing available memory")
	}
	self, e := os.ReadFile("/proc/self/cgroup")
	if e != nil {
		return 0, 0, e
	}
	mounts, e := os.ReadFile("/proc/self/mountinfo")
	if e != nil {
		return 0, 0, e
	}
	if dir, root, ok := memoryHierarchy(string(self), string(mounts)); ok {
		return availableInHierarchy(available, total, dir, root, "memory.max", "memory.current")
	}
	// Resolve the memory controller on v1 as well, including inherited limits.
	var process string
	for _, line := range strings.Split(string(self), "\n") {
		f := strings.SplitN(line, ":", 3)
		if len(f) == 3 && strings.Contains(","+f[1]+",", ",memory,") {
			process = f[2]
		}
	}
	if process == "" {
		return available, total, nil
	}
	for _, line := range strings.Split(string(mounts), "\n") {
		left, right, ok := strings.Cut(line, " - ")
		l, r := strings.Fields(left), strings.Fields(right)
		if !ok || len(l) < 5 || len(r) < 3 || r[0] != "cgroup" || !strings.Contains(","+r[2]+",", ",memory,") {
			continue
		}
		unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\134`, `\`)
		mountRoot, mountPoint := unescape.Replace(l[3]), unescape.Replace(l[4])
		if !safeHierarchyPath(process) || !safeHierarchyPath(mountRoot) || !safeHierarchyPath(mountPoint) {
			break
		}
		relative := strings.TrimPrefix(process, "/")
		if mountRoot != "/" && (process == mountRoot || strings.HasPrefix(process, mountRoot+"/")) {
			relative = strings.TrimPrefix(strings.TrimPrefix(process, mountRoot), "/")
		}
		return availableInHierarchy(available, total, filepath.Join(mountPoint, relative), mountPoint, "memory.limit_in_bytes", "memory.usage_in_bytes")
	}
	return 0, 0, errors.New("memory cgroup unavailable")
}

func availableInHierarchy(available, total int64, dir, root, limitName, usageName string) (int64, int64, error) {
	if !safeHierarchyPath(dir) || !safeHierarchyPath(root) || dir != root && !strings.HasPrefix(dir, root+"/") {
		return 0, 0, errors.New("invalid memory hierarchy")
	}
	for {
		raw, err := os.ReadFile(filepath.Join(dir, limitName))
		if err != nil {
			// The real v2 hierarchy root has no memory.max or cgroup.type. A
			// namespace root representing a non-root cgroup does have cgroup.type.
			if os.IsNotExist(err) && dir == root && limitName == "memory.max" {
				if _, e := os.Stat(filepath.Join(root, "cgroup.type")); os.IsNotExist(e) {
					return available, total, nil
				}
			}
			return 0, 0, err
		}
		value := strings.TrimSpace(string(raw))
		if value != "max" {
			limit, e := strconv.ParseUint(value, 10, 64)
			if e != nil {
				return 0, 0, e
			}
			if limit < 1<<62 {
				raw, e = os.ReadFile(filepath.Join(dir, usageName))
				if e != nil {
					return 0, 0, e
				}
				usage, e := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
				if e != nil {
					return 0, 0, e
				}
				remaining := int64(0)
				if usage < limit {
					remaining = int64(limit - usage)
				}
				available = min(available, remaining)
				total = min(total, int64(limit))
			}
		}
		if dir == root {
			return available, total, nil
		}
		dir = filepath.Dir(dir)
	}
}
