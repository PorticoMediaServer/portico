//go:build !linux

package mediaexec

import (
	"context"
	"os"
)

// Probe workers are a Linux bubblewrap feature (probe_pool_linux.go); other
// platforms start a sandbox per probe as before.

const ProbeWorkerFlag = "--portico-probe-worker"

type PooledProbe struct {
	Job         Job
	Input       *os.File
	OutputLimit int64
	LowPriority bool
}

type ProbeResult struct {
	Stdout []byte
	Stderr string
	Err    error
}

type ProbePoolStats struct {
	WorkersStarted uint64 `json:"workersStarted"`
	Jobs           uint64 `json:"jobs"`
	Retries        uint64 `json:"retries"`
	Idle           int    `json:"idle"`
}

func RunPooledProbe(context.Context, PooledProbe) (ProbeResult, bool, error) {
	return ProbeResult{}, false, nil
}

func ProbePoolSnapshot() ProbePoolStats { return ProbePoolStats{} }

func runProbeWorker([]string) error { return ErrInvalidJob }

func ProbePoolAccepts(Job) bool { return false }
