package dbwork

import "sync/atomic"

// coldReadDemand counts observed driver reader leases, including their WAL
// admission wait, above ClassMaintenance. Scan work at an interactive class (or
// BackgroundMedia) therefore outranks cold search too. Maintenance never counts
// itself. No extra permits, connections, SQL or reader admission are introduced.
var coldReadDemand atomic.Int64

// ColdReadShouldYield is an advisory process-wide snapshot for the lowest
// priority read preparation. Call BEFORE opening a read snapshot, with cold
// steps classified ClassMaintenance. A true answer means yield this quantum.
// It observes registered foreground requests, reader leases/admission waits,
// snapshot permit waiters and active/queued writers above maintenance. Writer
// mutex contention is conservatively treated as demand, without waiting.
//
// This does not reserve admission or preempt a running statement: demand can
// arrive immediately after the check. Direct reads waiting inside database/sql
// before driver admission are invisible unless an HTTP foreground probe covers
// them. Raw/uninstrumented handles are invisible. Existing shared implicit rows
// inherit their first lease's class until the connection's last rows close.
func ColdReadShouldYield() bool {
	if coldReadDemand.Load() > 0 || snapshotWaiting.Load() > 0 || len(foregroundSnapshotPermits) > 0 || coldForegroundActive() {
		return true
	}
	g := WriteGate()
	if !g.mu.TryLock() {
		return true
	}
	defer g.mu.Unlock()
	if g.active && g.holderClass.Priority() < ClassMaintenance.Priority() {
		return true
	}
	for class := ClassSecurityFence; class < ClassMaintenance; class++ {
		if g.queues[class].count > 0 {
			return true
		}
	}
	return false
}

// Probe registration contention is itself a reason to defer; never wait for a
// bookkeeping lock on this optional background path. Registered probes obey
// RegisterForegroundProbe's memory-only contract.
func coldForegroundActive() bool {
	if !pressureMu.TryRLock() {
		return true
	}
	defer pressureMu.RUnlock()
	for _, probe := range foregroundProbes {
		if probe() {
			return true
		}
	}
	return false
}
