//go:build linux || windows

package telemetry

import (
	"context"
	"strconv"
	"strings"
)

// nvidiaReading queries the NVIDIA driver's own reporter, which is the same
// executable and the same query on Linux and Windows. It is the only reporter
// that publishes encoder utilisation, which is the figure an owner actually
// wants when asking whether hardware transcoding is keeping up.
func nvidiaReading(ctx context.Context) (gpuReading, bool) {
	if !commandAvailable("nvidia-smi") {
		return gpuReading{}, false
	}
	raw, e := runCommand(ctx, "nvidia-smi", "--query-gpu=name,utilization.gpu,utilization.memory,utilization.encoder", "--format=csv,noheader,nounits")
	if e != nil {
		return unknownGPU("The NVIDIA reporter did not answer within its time budget."), true
	}
	line, _, _ := strings.Cut(strings.TrimSpace(raw), "\n")
	parts := strings.Split(line, ",")
	if len(parts) < 4 {
		return unknownGPU("The NVIDIA reporter returned an unreadable row."), true
	}
	out := gpuReading{provider: "NVIDIA", device: strings.TrimSpace(parts[0])}
	for index, target := range []*Metric{&out.usage, &out.memory, &out.encoder} {
		value, e := strconv.ParseFloat(strings.TrimSpace(parts[index+1]), 64)
		if e != nil {
			*target = Unavailable("The NVIDIA reporter did not publish this figure.")
			continue
		}
		*target = Available(percent(value))
	}
	return out, true
}
