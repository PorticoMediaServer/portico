//go:build linux

package telemetry

import (
	"context"
	"strconv"
	"strings"
	"syscall"
)

// Linux exposes everything this package needs through procfs and sysfs, so no
// reporter process is started for host facts.

func sampleCPU(ctx context.Context, previous *cpuCounter) Metric {
	raw, e := readFileBounded("/proc/stat")
	if e != nil {
		return Unavailable("Linux did not expose /proc/stat.")
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(line)
		if len(parts) < 5 || parts[0] != "cpu" {
			continue
		}
		var total, idle uint64
		for index, value := range parts[1:] {
			n, e := strconv.ParseUint(value, 10, 64)
			if e != nil {
				return Unavailable("Linux reported unreadable CPU time.")
			}
			total += n
			if index == 3 || index == 4 {
				idle += n
			}
		}
		return deltaCPU(previous, total-idle, total)
	}
	return Unavailable("Linux did not report aggregate CPU time.")
}

func sampleMemory(ctx context.Context) (Metric, int64, int64) {
	raw, e := readFileBounded("/proc/meminfo")
	if e != nil {
		return Unavailable("Linux did not expose /proc/meminfo."), 0, 0
	}
	values := map[string]int64{}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(line)
		if len(parts) < 2 {
			continue
		}
		n, e := strconv.ParseInt(parts[1], 10, 64)
		if e != nil {
			continue
		}
		values[strings.TrimSuffix(parts[0], ":")] = n * 1024
	}
	total, available := values["MemTotal"], values["MemAvailable"]
	if total <= 0 || available <= 0 || available > total {
		return Unavailable("Linux did not report total and available memory."), 0, 0
	}
	used := total - available
	return Available(percent(float64(used) / float64(total) * 100)), used, total
}

// readDiskCounter reports the block device backing the state directory. The
// state volume is the one whose pressure matters to this server; summing every
// device would report unrelated hardware as the server's own load.
func readDiskCounter(ctx context.Context, state string) (counter, string) {
	if state == "" {
		return counter{}, "The state directory is not registered, so no volume can be measured."
	}
	var info syscall.Stat_t
	if syscall.Stat(state, &info) != nil {
		return counter{}, "The state volume could not be identified."
	}
	device := uint64(info.Dev)
	major, minor := device>>8&0xfff, device&0xff|device>>12&^uint64(0xff)
	raw, e := readFileBounded("/proc/diskstats")
	if e != nil {
		return counter{}, "Linux did not expose /proc/diskstats."
	}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(line)
		if len(parts) < 10 {
			continue
		}
		lineMajor, e1 := strconv.ParseUint(parts[0], 10, 64)
		lineMinor, e2 := strconv.ParseUint(parts[1], 10, 64)
		if e1 != nil || e2 != nil || lineMajor != major || lineMinor != minor {
			continue
		}
		read, e1 := strconv.ParseUint(parts[5], 10, 64)
		write, e2 := strconv.ParseUint(parts[9], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		// diskstats counts 512-byte sectors regardless of the device's own
		// sector size, which is the kernel's documented unit.
		return counter{read: read * 512, write: write * 512, known: true}, ""
	}
	return counter{}, "Linux does not publish I/O counters for the device backing the state volume."
}

func readNetCounter(ctx context.Context) (counter, string) {
	raw, e := readFileBounded("/proc/net/dev")
	if e != nil {
		return counter{}, "Linux did not expose /proc/net/dev."
	}
	var in, out uint64
	found := false
	for _, line := range strings.Split(raw, "\n") {
		name, rest, ok := strings.Cut(line, ":")
		name = strings.TrimSpace(name)
		if !ok || name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") {
			continue
		}
		parts := fields(rest)
		if len(parts) < 9 {
			continue
		}
		received, e1 := strconv.ParseUint(parts[0], 10, 64)
		sent, e2 := strconv.ParseUint(parts[8], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		in, out, found = in+received, out+sent, true
	}
	if !found {
		return counter{}, "Linux reported no non-loopback interface counters."
	}
	return counter{read: in, write: out, known: true}, ""
}
