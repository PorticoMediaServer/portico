//go:build linux

package telemetry

import (
	"context"
	"encoding/json"
	"strings"
)

func sampleGPU(ctx context.Context) gpuReading {
	if reading, ok := nvidiaReading(ctx); ok {
		return reading
	}
	if reading, ok := intelReading(ctx); ok {
		return reading
	}
	return unknownGPU("No GPU reporter is installed. Install the vendor's management tool, and in a container pass the GPU device and its driver interfaces through.")
}

// intelReading reads intel_gpu_top's JSON stream. The tool streams samples, so
// one period is requested and the first complete object is taken; the bounded
// command runner ends it either way.
func intelReading(ctx context.Context) (gpuReading, bool) {
	if !commandAvailable("intel_gpu_top") {
		return gpuReading{}, false
	}
	raw, e := runCommand(ctx, "intel_gpu_top", "-J", "-s", "200", "-n", "1")
	if e != nil || strings.TrimSpace(raw) == "" {
		return unknownGPU("The Intel GPU reporter did not answer. It usually requires membership of the render group."), true
	}
	out := gpuReading{provider: "Intel", device: "Intel GPU"}
	out.usage = Unavailable("The Intel reporter did not publish overall utilisation.")
	out.memory = Unavailable("The Intel reporter does not publish GPU memory utilisation.")
	out.encoder = Unavailable("The Intel reporter did not publish encoder utilisation.")
	var document struct {
		Engines map[string]struct {
			Busy float64 `json:"busy"`
		} `json:"engines"`
	}
	if json.Unmarshal([]byte(firstJSONObject(raw)), &document) != nil || len(document.Engines) == 0 {
		return out, true
	}
	var highest float64
	for name, engine := range document.Engines {
		if engine.Busy > highest {
			highest = engine.Busy
		}
		if strings.HasPrefix(name, "Video/") && !strings.HasPrefix(name, "VideoEnhance") {
			out.encoder = Available(percent(engine.Busy))
		}
	}
	out.usage = Available(percent(highest))
	return out, true
}

// firstJSONObject extracts one balanced object from a stream that may contain
// several, which is how intel_gpu_top reports successive periods.
func firstJSONObject(raw string) string {
	depth, start := 0, -1
	for index, r := range raw {
		switch r {
		case '{':
			if depth == 0 {
				start = index
			}
			depth++
		case '}':
			depth--
			if depth == 0 && start >= 0 {
				return raw[start : index+1]
			}
		}
	}
	return "{}"
}
