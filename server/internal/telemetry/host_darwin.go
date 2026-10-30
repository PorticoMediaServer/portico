//go:build darwin

package telemetry

import (
	"context"
	"strconv"
	"strings"
)

// macOS exposes far less than Linux without elevated access. Where a figure can
// only be derived, it is reported as limited with the derivation named, so an
// owner reading the Server overview knows what they are looking at. powermetrics
// is deliberately not used: it requires elevated access this server never takes.

func sampleCPU(ctx context.Context, previous *cpuCounter) Metric {
	return Unavailable("macOS does not expose host processor utilization through this sampler.")
}

func sampleMemory(ctx context.Context) (Metric, int64, int64) {
	totalRaw, e := runCommand(ctx, "sysctl", "-n", "hw.memsize")
	if e != nil {
		return Unavailable("macOS did not report installed memory."), 0, 0
	}
	total, e := strconv.ParseInt(strings.TrimSpace(totalRaw), 10, 64)
	if e != nil || total <= 0 {
		return Unavailable("macOS reported an unreadable memory size."), 0, 0
	}
	stat, e := runCommand(ctx, "vm_stat")
	if e != nil {
		return Unavailable("macOS did not expose virtual memory statistics."), 0, 0
	}
	pageSize := int64(4096)
	pages := map[string]int64{}
	for _, line := range strings.Split(stat, "\n") {
		if strings.Contains(line, "page size of") {
			for _, part := range fields(line) {
				if n, e := strconv.ParseInt(part, 10, 64); e == nil && n > 0 {
					pageSize = n
				}
			}
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		n, e := strconv.ParseInt(strings.TrimSuffix(strings.TrimSpace(value), "."), 10, 64)
		if e != nil {
			continue
		}
		pages[strings.TrimSpace(key)] = n
	}
	used := (pages["Pages active"] + pages["Pages wired down"] + pages["Pages occupied by compressor"]) * pageSize
	if used <= 0 || used > total {
		return Unavailable("macOS did not report enough page counts to compute used memory."), 0, 0
	}
	return Available(percent(float64(used) / float64(total) * 100)), used, total
}

// macOS publishes no per-volume read and write counters to an unprivileged
// process. Reporting nothing is correct: a combined whole-device figure would
// not answer what an owner is asking about their state volume.
func readDiskCounter(context.Context, string) (counter, string) {
	return counter{}, "macOS does not expose per-volume read and write counters without elevated access."
}

func readNetCounter(ctx context.Context) (counter, string) {
	raw, e := runCommand(ctx, "netstat", "-ib")
	if e != nil {
		return counter{}, "macOS did not expose interface byte counters."
	}
	var received, sent uint64
	seen := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		parts := fields(line)
		if len(parts) < 11 || !strings.HasPrefix(parts[2], "<Link") {
			continue
		}
		name := parts[0]
		if name == "lo0" || seen[name] {
			continue
		}
		in, e1 := strconv.ParseUint(parts[6], 10, 64)
		out, e2 := strconv.ParseUint(parts[9], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		seen[name] = true
		received, sent = received+in, sent+out
	}
	if len(seen) == 0 {
		return counter{}, "macOS reported no non-loopback interface counters."
	}
	return counter{read: received, write: sent, known: true}, ""
}
