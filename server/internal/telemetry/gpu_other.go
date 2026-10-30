//go:build !linux && !darwin && !windows

package telemetry

import "context"

func sampleGPU(context.Context) gpuReading {
	return unknownGPU("GPU telemetry is not supported on this platform.")
}
