package writer

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// Installed only by trusted composition alongside its funded keeper. The
// existing bounded maintenance channel owns observations, not writer authority.
type checkpointMaintenanceRecord struct {
	Result   governor.CheckpointResult `json:"result"`
	Error    string                    `json:"error"`
	Deferral string                    `json:"deferral,omitempty"`
}

func checkpointRecord(out governor.CheckpointResult, err error) checkpointMaintenanceRecord {
	r := checkpointMaintenanceRecord{Result: out}
	if err != nil {
		r.Error = err.Error()
	}
	if out.Deferred {
		r.Deferral = "native_busy"
	} else if errors.Is(err, governor.ErrCheckpointCadence) && out.Started.IsZero() && out.NativeStarted.IsZero() {
		// Pressure maintenance can advance the shared cadence while a periodic
		// offer waits. No grant/native call occurred; keep the typed observation.
		r.Deferral = "cadence"
	} else if errors.Is(err, governor.ErrCheckpointForeground) && out.Started.IsZero() && out.NativeStarted.IsZero() {
		// The screening experiment deferred this start for an in-flight Foreground
		// writer. No grant/native call occurred; it is a deferral, not a failure.
		r.Deferral = "foreground"
	}
	return r
}

func (r checkpointMaintenanceRecord) failed() bool {
	if (r.Deferral == "cadence" || r.Deferral == "foreground") && r.Result.Started.IsZero() && r.Result.NativeStarted.IsZero() {
		return false
	}
	return r.Error != "" || r.Result.Refusal != nil || !r.Result.PhysicalSafe
}

type checkpointMaintenance struct {
	request governor.Request
	results chan<- checkpointMaintenanceRecord
}

func (r *effectiveRuntime) prepareCheckpoint(ctx context.Context, next governor.Request) (bool, error) {
	if r.maintenance == nil {
		return false, nil
	}
	service := r.executor.keeper.Load()
	if service == nil {
		return false, governor.ErrCheckpointKeeper
	}
	request := r.maintenance.request
	// The template owns the forecast, never a renewable deadline. Both the
	// caller and next immutable admission deadline cap this maintenance offer.
	request.Deadline = next.Deadline
	out, err := service.PrepareWriter(ctx, request, next)
	if out.Recovery {
		record := checkpointRecord(out, err)
		select {
		case r.maintenance.results <- record:
		default:
			return true, errors.New("checkpoint maintenance observation capacity exceeded")
		}
	}
	if err == nil && out.Deferred {
		err = governor.ErrCheckpointDeferred // no reset credit and no command retry
	}
	return out.Recovery, err
}

// Refresh AFTER any maintenance, BEFORE Try. Its old sample/risk cannot admit a
// writer after recovery. Keep the original deadline even if maintenance waited.
func (r *effectiveRuntime) refreshAfterCheckpoint(ctx context.Context, expected *governor.Request) (*WALObservation, error) {
	observation, err := observeWriterWALAt(r.path, r.wal, r.executor.now, &r.clockHighWater)
	if err != nil {
		return nil, err
	}
	now := r.executor.now()
	if err := observeTime(&r.clockHighWater, now); err != nil {
		return nil, err
	}
	expected.RestartRisk, err = r.wal.RestartRiskContext(ctx, now, expected.Deadline)
	return observation, err
}

// bindCheckpointKeeper is trusted startup composition only. openScanStore has
// attested these values against the actual opened native file and re-attests
// every replacement. No writable handle leaves W and no worker can bind one.
func (o *scanOpening) bindCheckpointKeeper(k *governor.CheckpointKeeper, g governor.Governor, a *governor.Authority, wal *governor.WALGate) error {
	if o == nil || o.executor == nil || o.binding == nil {
		return ErrInvalid
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	w := o.executor
	if !w.mu.TryLock() {
		return ErrBusy
	}
	defer w.mu.Unlock()
	if o.closed || w.closed || w.keeper.Load() != nil {
		return ErrClosed
	}
	if err := o.binding.check(); err != nil {
		return err
	}
	service, err := k.BindWriter(g, a, wal, o.binding.device, o.binding.inode)
	if err != nil {
		return err
	}
	w.keeper.Store(service)
	return nil
}
