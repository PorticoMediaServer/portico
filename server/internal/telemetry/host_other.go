//go:build !linux && !darwin && !windows

package telemetry

import "context"

// Every other platform reports honestly that it has no supported reporter,
// which the admin overview renders as an unavailable metric rather than a zero.

const unsupportedHost = "Host telemetry is not supported on this platform."

func sampleCPU(context.Context, *cpuCounter) Metric { return Unavailable(unsupportedHost) }
func sampleMemory(context.Context) (Metric, int64, int64) {
	return Unavailable(unsupportedHost), 0, 0
}
func readDiskCounter(context.Context, string) (counter, string) {
	return counter{}, unsupportedHost
}
func readNetCounter(context.Context) (counter, string) { return counter{}, unsupportedHost }
