//go:build darwin

package telemetry

import (
	"context"
	"regexp"
	"strconv"
	"strings"
)

// macOS publishes GPU counters through IOAccelerator, which an unprivileged
// process can read with ioreg. What it exposes varies by hardware, and it never
// exposes encoder utilisation, so those metrics say so rather than read zero.
// powermetrics would expose more but requires elevated access.

const appleGPULimit = "macOS exposes only what IOAccelerator publishes for this hardware; it does not report encoder utilisation."

func sampleGPU(ctx context.Context) gpuReading {
	raw, e := runCommand(ctx, "ioreg", "-r", "-d", "1", "-w", "0", "-c", "IOAccelerator")
	if e != nil || strings.TrimSpace(raw) == "" {
		return unknownGPU("macOS did not expose GPU performance statistics for this hardware.")
	}
	out := unknownGPU(appleGPULimit)
	out.device = ioregText(raw, "model")
	if out.device == "" {
		out.device = "Integrated GPU"
	}
	out.provider = "Apple"
	if !strings.Contains(strings.ToLower(out.device), "apple") {
		out.provider = "Integrated"
	}
	usage, ok := ioregNumber(raw, "Device Utilization %")
	if !ok {
		renderer, rendererOK := ioregNumber(raw, "Renderer Utilization %")
		tiler, tilerOK := ioregNumber(raw, "Tiler Utilization %")
		if rendererOK || tilerOK {
			usage, ok = renderer, true
			if tiler > usage {
				usage = tiler
			}
		}
	}
	if ok {
		out.usage = Limited(percent(usage), appleGPULimit)
	}
	allocated, allocatedOK := ioregNumber(raw, "Alloc system memory")
	inUse, inUseOK := ioregNumber(raw, "In use system memory")
	if allocatedOK && inUseOK && allocated > 0 {
		out.memory = Limited(percent(inUse/allocated*100), appleGPULimit)
	}
	return out
}

func ioregText(raw, key string) string {
	match := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*"([^"]{1,200})"`).FindStringSubmatch(raw)
	if len(match) != 2 {
		return ""
	}
	return strings.TrimSpace(match[1])
}

func ioregNumber(raw, key string) (float64, bool) {
	match := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*=\s*([0-9]+(?:\.[0-9]+)?)`).FindStringSubmatch(raw)
	if len(match) != 2 {
		return 0, false
	}
	value, e := strconv.ParseFloat(match[1], 64)
	return value, e == nil
}
