//go:build linux

package telemetry

import (
	"context"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type processCounter struct {
	at     int64
	micros int64
}

// CPU is process CPU time divided by wall time and logical CPUs; memory is OS
// resident pages divided by physical host memory. Neither is a Go heap metric.
func sampleProcess(ctx context.Context, previous *processCounter, at, total int64) (Metric, Metric) {
	var usage syscall.Rusage
	cpu := Unavailable("The operating system did not report process CPU time.")
	if syscall.Getrusage(syscall.RUSAGE_SELF, &usage) == nil {
		micros := usage.Utime.Sec*1000000 + usage.Utime.Usec + usage.Stime.Sec*1000000 + usage.Stime.Usec
		if previous.at > 0 && at > previous.at && micros >= previous.micros {
			cpu = Available(percent(float64(micros-previous.micros) / float64((at-previous.at)*1000) / float64(runtime.NumCPU()) * 100))
		} else {
			cpu = Unavailable(warmingUp)
		}
		*previous = processCounter{at, micros}
	}
	mem := Unavailable("The operating system did not report process resident memory.")
	raw, err := readFileBounded("/proc/self/statm")
	parts := strings.Fields(raw)
	if err == nil && len(parts) > 1 && total > 0 {
		pages, e := strconv.ParseUint(parts[1], 10, 64)
		if e == nil {
			mem = Available(percent(float64(pages) * float64(syscall.Getpagesize()) / float64(total) * 100))
		}
	}
	return cpu, mem
}
