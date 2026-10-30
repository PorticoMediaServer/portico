//go:build windows

package telemetry

import "context"

// Windows publishes GPU engine utilisation only through performance counters,
// whose collection costs more than this sampler's budget. The NVIDIA reporter
// is used where it is installed; otherwise the metrics say what is missing.
func sampleGPU(ctx context.Context) gpuReading {
	if reading, ok := nvidiaReading(ctx); ok {
		return reading
	}
	return unknownGPU("No GPU reporter is installed. Install the vendor's management tool, such as the NVIDIA driver utilities, to publish GPU utilisation.")
}
