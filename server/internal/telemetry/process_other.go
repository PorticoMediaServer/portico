//go:build !linux

package telemetry

import "context"

type processCounter struct{}

func sampleProcess(context.Context, *processCounter, int64, int64) (Metric, Metric) {
	return Unavailable("Process CPU measurement is not supported on this platform."), Unavailable("Process resident memory measurement is not supported on this platform.")
}
