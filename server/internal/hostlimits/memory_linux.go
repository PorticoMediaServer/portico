//go:build linux

package hostlimits

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func physicalMemoryBytes() (uint64, bool) {
	file, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer file.Close()
	return parsePhysicalMemory(file)
}

func parsePhysicalMemory(reader io.Reader) (uint64, bool) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 || fields[0] != "MemTotal:" || fields[2] != "kB" {
			continue
		}
		value, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil || value == 0 || value > (1<<63-1)/1024 {
			return 0, false
		}
		return value * 1024, true
	}
	return 0, false
}

// With the default GOGC the heap grows to twice the live set before a
// collection. Inside a Docker memory limit, or on a 4 GiB home server, the
// kernel's OOM killer wins that race and the process dies with no log line at
// all — the worst possible failure, because the owner is left with "it restarted
// and I don't know why". debug.SetMemoryLimit turns the same pressure into more
// frequent collection and a slower server, which is a thing an owner can see and
// act on.
//
// The limit has to come from the cgroup rather than from total system memory: a
// container is told how much it may use, and that is the only number that
// matters. Where there is no such number there is nothing to derive, and a
// guessed limit set too low is a GC death spiral — so nothing is set.

// cgroupMemoryLimit reads this process's memory ceiling, in bytes.
func cgroupMemoryLimit() (uint64, bool) {
	// cgroup v2 first: the unified hierarchy is what every current distribution
	// and Docker release uses.
	if self, err := os.ReadFile("/proc/self/cgroup"); err == nil {
		if mounts, err := os.ReadFile("/proc/self/mountinfo"); err == nil {
			if dir, root, ok := memoryHierarchy(string(self), string(mounts)); ok {
				if value, ok := hierarchyMemoryLimit(dir, root); ok {
					return value, true
				}
			}
		}
	}
	// v1, for older hosts.
	for _, path := range []string{
		"/sys/fs/cgroup/memory/memory.limit_in_bytes",
		"/sys/fs/cgroup/memory.max",
	} {
		if value, ok := readMemoryValue(path); ok {
			return value, true
		}
	}
	return 0, false
}

// memoryHierarchy accounts for both a host mount rooted at / and a container
// mount rooted at an ancestor. Cgroup namespaces may report the process path
// relative to that mount; no candidate may escape the actual mount point.
func memoryHierarchy(self, mounts string) (dir, root string, ok bool) {
	var process string
	for _, line := range strings.Split(self, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) == 3 && fields[0] == "0" && fields[1] == "" {
			process = fields[2]
			break
		}
	}
	if !safeHierarchyPath(process) {
		return "", "", false
	}
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for _, line := range strings.Split(mounts, "\n") {
		left, right, found := strings.Cut(line, " - ")
		fields, filesystem := strings.Fields(left), strings.Fields(right)
		if !found || len(fields) < 5 || len(filesystem) < 1 || filesystem[0] != "cgroup2" {
			continue
		}
		mountRoot, mountPoint := unescape.Replace(fields[3]), unescape.Replace(fields[4])
		if !safeHierarchyPath(mountRoot) || !safeHierarchyPath(mountPoint) {
			continue
		}
		relative := strings.TrimPrefix(process, "/")
		if mountRoot != "/" && (process == mountRoot || strings.HasPrefix(process, mountRoot+"/")) {
			relative = strings.TrimPrefix(strings.TrimPrefix(process, mountRoot), "/")
		}
		return filepath.Join(mountPoint, relative), filepath.Clean(mountPoint), true
	}
	return "", "", false
}

func safeHierarchyPath(path string) bool {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, '\x00') {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." || part == "." {
			return false
		}
	}
	return true
}

func hierarchyMemoryLimit(dir, root string) (uint64, bool) {
	if !safeHierarchyPath(dir) || !safeHierarchyPath(root) || dir != root && !strings.HasPrefix(dir, root+"/") {
		return 0, false
	}
	var minimum uint64
	for {
		if value, ok := readMemoryValue(filepath.Join(dir, "memory.max")); ok && (minimum == 0 || value < minimum) {
			minimum = value
		}
		if dir == root {
			return minimum, minimum > 0
		}
		dir = filepath.Dir(dir)
	}
}

// readMemoryValue reads one cgroup byte value, rejecting the several spellings
// of "no limit".
func readMemoryValue(path string) (uint64, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	text := strings.TrimSpace(string(raw))
	if text == "" || text == "max" {
		return 0, false
	}
	value, err := strconv.ParseUint(text, 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	// cgroup v1 writes a number near 2^63 to mean unlimited, and a limit larger
	// than any real machine is the same statement.
	if value >= 1<<62 {
		return 0, false
	}
	return value, true
}
