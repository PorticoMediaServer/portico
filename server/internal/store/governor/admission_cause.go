package governor

import (
	"context"
	"time"
)

// RefusalCause identifies actual admission state/budget, never mutex contention.
type RefusalCause string

const (
	CauseInvalidRequest    RefusalCause = "invalid_request"
	CauseDeadline          RefusalCause = "original_deadline"
	CauseUnqualified       RefusalCause = "unqualified_forecast"
	CauseStatic            RefusalCause = "static_epoch"
	CauseRecoveryDrain     RefusalCause = "recovery_drain"
	CauseGrantCapacity     RefusalCause = "grant_metadata_capacity"
	CausePartition         RefusalCause = "resource_partition"
	CauseWriterActive      RefusalCause = "physical_writer_active"
	CauseDeviceActive      RefusalCause = "physical_device_active"
	CausePreparation       RefusalCause = "preparation_conflict"
	CauseKeeper            RefusalCause = "keeper_unavailable"
	CauseCheckpointActive  RefusalCause = "checkpoint_active"
	CauseCheckpointTicket  RefusalCause = "checkpoint_ticket"
	CauseCheckpointCadence RefusalCause = "checkpoint_cadence"
	CauseWALSample         RefusalCause = "wal_sample_unavailable"
	CauseWALRisk           RefusalCause = "wal_restart_forecast_stale"
	CauseWALPhysical       RefusalCause = "wal_physical_hard_bound"
	CauseWALDebt           RefusalCause = "wal_debt_pressure"
	CauseWALActive         RefusalCause = "wal_active_pressure"
	CauseWALSpace          RefusalCause = "wal_append_space"
	CauseShutdown          RefusalCause = "keeper_shutdown_state"
)

// AdmissionError preserves the existing error identity while reporting why a
// maintenance offer could not proceed. It is not a grant or a retry mechanism.
type AdmissionError struct {
	Cause RefusalCause
	Err   error
}

func (e *AdmissionError) Error() string            { return string(e.Cause) + ": " + e.Err.Error() }
func (e *AdmissionError) Unwrap() error            { return e.Err }
func admissionError(c RefusalCause, e error) error { return &AdmissionError{Cause: c, Err: e} }

// Lock handoff consumes the original budget. No goroutine, polling queue or
// renewed deadline; the same ledger/gate remains the only admission authority.
func (m *ledgerMutex) lockUntil(ctx context.Context, deadline, now time.Time) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !deadline.After(now) {
		return context.DeadlineExceeded
	}
	if m.TryLock() {
		return nil
	}
	if err := m.lockWithin(deadline.Sub(now), nil, ctx); err != nil {
		if err == ErrGrant {
			return context.DeadlineExceeded
		}
		return err
	}
	return nil
}
