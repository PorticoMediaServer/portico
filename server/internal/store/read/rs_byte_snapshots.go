package read

import (
	"context"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

type rsForecastSource interface {
	browseSnapshots
	ReadForecasted(context.Context, governor.Request, time.Duration, governor.SnapshotForecast, func(*governor.ReadScope) error) (governor.SnapshotResult, error)
}

// rsByteWindow selects the EXISTING forecasted entry for the next business
// window, after scalar points measured actual bytes in the initial snapshot.
// QueryExactBytes rechecks lengths in that business snapshot before decoding.
// Common initial/final fences retain their existing separate read recipe.
// This adapter creates no grant, ledger, queue, pool or lifecycle authority.
type rsByteWindow struct {
	source  browseSnapshots
	pending bool
	bytes   int64
	calls   uint16
}

func (w *rsByteWindow) NativeRowReservation() int64 { return w.source.NativeRowReservation() }
func (w *rsByteWindow) arm(bytes int64, calls uint16) error {
	if bytes <= 0 || bytes > w.source.NativeRowReservation() || calls == 0 {
		return governor.ErrSnapshotBudget
	}
	w.bytes, w.calls, w.pending = bytes, calls, true
	return nil
}
func (w *rsByteWindow) Read(ctx context.Context, r governor.Request, budget time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	if w.pending {
		w.pending = false
		source, ok := w.source.(rsForecastSource)
		if !ok {
			return governor.SnapshotResult{}, ErrInput
		}
		return source.ReadForecasted(ctx, r, budget, governor.SnapshotForecast{NativeRowBytes: w.bytes, MaxQueries: w.calls}, read)
	}
	result, err := w.source.Read(ctx, r, budget, read)
	if err != nil || result.Refusal != nil {
		w.pending = false
	}
	return result, err
}
func rsPointBytes(row []any) (int64, error) {
	var total int64
	for _, v := range row {
		if v == nil {
			continue
		}
		n, ok := v.(int64)
		if !ok || n < 0 || n > (1<<62)-total {
			return 0, governor.ErrSnapshotBudget
		}
		total += n
	}
	return total, nil
}
