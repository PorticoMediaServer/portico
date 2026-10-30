package dbwork

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

var (
	pressureMu sync.RWMutex
	// foregroundProbes are registered by the HTTP admission layer, keyed by
	// source. Each reports whether any foreground request is in flight in a lane
	// that competes with bulk work. Keying by source means a replacement handler
	// replaces its predecessor's signal instead of accumulating beside it.
	foregroundProbes = map[string]func() bool{}
	// healthProbe reports whether the database is healthy enough for background
	// writes. Nil means healthy.
	healthProbe    func() bool
	yields         atomic.Uint64
	yieldWaitNanos atomic.Uint64
)

// RegisterForegroundProbe adds a memory-only signal that foreground work is in
// flight. It must not touch the database: the earlier design rebuilt full
// resource diagnostics on every item, which made the scanner its own source of
// the pressure it was measuring.
func RegisterForegroundProbe(source string, probe func() bool) {
	pressureMu.Lock()
	if probe == nil {
		delete(foregroundProbes, source)
	} else {
		foregroundProbes[source] = probe
	}
	pressureMu.Unlock()
}

// RegisterHealthProbe installs the database health gate consulted before any
// background batch.
func RegisterHealthProbe(probe func() bool) {
	pressureMu.Lock()
	healthProbe = probe
	pressureMu.Unlock()
}

// ResetProbes clears every registered probe. Tests use it; nothing else should.
func ResetProbes() {
	pressureMu.Lock()
	foregroundProbes = map[string]func() bool{}
	healthProbe = nil
	pressureMu.Unlock()
}

// ForegroundWorkActive reports whether foreground traffic is in flight. It is
// deliberately memory-only and allocation-free on the common path.
func ForegroundWorkActive() bool {
	pressureMu.RLock()
	defer pressureMu.RUnlock()
	for _, probe := range foregroundProbes {
		if probe() {
			return true
		}
	}
	return false
}

// HealthAllowsBackground reports whether the database is well enough for bulk
// writes to continue.
func HealthAllowsBackground() bool {
	pressureMu.RLock()
	probe := healthProbe
	pressureMu.RUnlock()
	return probe == nil || probe()
}

// Yield is a cancellation boundary, never an admission gate. Background work
// keeps progressing under foreground load; the writer queue and OS scheduling
// apply its lower priority at the actual contested resource.
func Yield(ctx context.Context) bool {
	if ForegroundWorkActive() {
		yields.Add(1)
	}
	return WaitBackground(ctx)
}

// PressureStats reports how much background work has actually deferred.
type PressureStats struct {
	Yields          uint64 `json:"yields"`
	YieldWaitMillis uint64 `json:"yieldWaitMillis"`
	ValveBlocks     uint64 `json:"valveBlocks"`
	Foreground      bool   `json:"foregroundActive"`
	Healthy         bool   `json:"healthy"`
}

// Pressure snapshots the yield counters.
func Pressure() PressureStats {
	return PressureStats{
		Yields:          yields.Load(),
		YieldWaitMillis: yieldWaitNanos.Load() / uint64(time.Millisecond),
		ValveBlocks:     0,
		Foreground:      ForegroundWorkActive(),
		Healthy:         HealthAllowsBackground(),
	}
}
