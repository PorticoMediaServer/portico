package governor

import (
	"context"
	"time"
)

// WALRestartRisk is values-only prospective work metadata, never authority. A
// fully backfilled prefix MAY restart; readers can still prevent it. A zero
// value preserves the older, nonintegrated bridge and makes no cost claim.
// W must supply a qualified whole-command header-sync forecast, not an assumed
// 5ms maximum. Retained physical allocation does not remove the header sync.
type WALRestartRisk struct {
	Observation uint64
	ObservedAt  time.Time
	Possible    bool
	HeaderBytes uint32
}

// RestartRisk performs no IO. Invalid/stale/unavailable samples are explicit
// errors, never an implicit "warm" answer. Carry the result in Request through
// ordinary Try: reserve revalidates the generation atomically with admission.
func (w *WALGate) RestartRisk(now time.Time) (WALRestartRisk, error) {
	return w.RestartRiskContext(context.Background(), now, now.Add(w.policy.MaxSampleAge))
}

// RestartRiskContext bounds lock handoff by the original command deadline.
func (w *WALGate) RestartRiskContext(ctx context.Context, now, deadline time.Time) (WALRestartRisk, error) {
	if err := w.mu.lockUntil(ctx, deadline, now); err != nil {
		return WALRestartRisk{}, admissionError(admissionWaitCause(err), err)
	}
	defer w.mu.Unlock()
	if !w.fresh(now) || w.writer || (w.checkpoint && !w.policy.ConcurrentPassive) || (w.keeper != nil && !w.keeper.active(now)) {
		return WALRestartRisk{}, admissionError(CauseWALSample, ErrWALObservation)
	}
	return w.restartRisk(), nil
}
func (w *WALGate) fresh(now time.Time) bool {
	return w.valid && !now.Before(w.at) && now.Sub(w.at) <= w.policy.MaxSampleAge
}
func (w *WALGate) restartRisk() WALRestartRisk {
	r := WALRestartRisk{Observation: w.version, ObservedAt: w.at, Possible: w.policy.ConcurrentPassive || w.state.Frames == w.state.Backfill}
	if r.Possible {
		r.HeaderBytes = 32
	}
	return r
}

// ReadyWriter is constructed ONLY by the trusted closed-purpose dispatcher.
// Eligible includes all existing readiness/deadline/profile/service checks.
// This value grants no command, priority, SQL, receipt or resource authority.
type ReadyWriter struct {
	Class    Class
	Eligible bool
}

// ChooseRestartWriter is an immediate bounded preference over ALREADY ready
// candidates. No background candidate means immediate original-order fallback,
// including Foreground/FULL Recovery; it never waits for future work. The
// dispatcher must still revalidate the selected request via ordinary Try/Check.
func ChooseRestartWriter(risk WALRestartRisk, ready []ReadyWriter) (int, error) {
	if risk.ObservedAt.IsZero() || (risk.Possible && risk.HeaderBytes != 32) || (!risk.Possible && risk.HeaderBytes != 0) || len(ready) > 32 {
		return -1, ErrWALObservation
	}
	first := -1
	for i, candidate := range ready {
		if candidate.Class > Background {
			return -1, ErrGrant
		}
		if candidate.Eligible && first < 0 {
			first = i
		}
	}
	if risk.Possible {
		for i, candidate := range ready {
			if candidate.Eligible && (candidate.Class == Background || candidate.Class == ScanMinimum) {
				return i, nil
			}
		}
	}
	return first, nil
}

// WALRestartObservation records header creation or actual salt/frame reuse.
// WriterKnown requires the exact preceding admitted writer's physical return
// and first subsequent successful sample; otherwise Class/Forecast are unknown.
// Complete observed hold/xWrite/xSync receipts remain the W adapter's job.
type WALRestartObservation struct {
	Sequence         uint64
	ObservedAt       time.Time
	InitialHeader    bool
	WriterKnown      bool
	Class            Class
	ForecastHold     time.Duration
	ForecastIOBytes  int64
	ForecastWALBytes int64
	Risk             WALRestartRisk
	Before, After    WALState
}

func (w *WALGate) LastRestartObservation() WALRestartObservation {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastRestart
}
