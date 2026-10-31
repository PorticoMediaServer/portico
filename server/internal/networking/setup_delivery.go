package networking

import (
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// setupDelivery is what this server knows about applying an approval it was given:
// when it arrived, how many tries have failed, when the next starts, and whether
// anything is still trying. It lives in memory; a restart re-arms the wait, so after a
// restart `since` (and with it "slow") counts from when the approval was seen again.
// An entry goes when the claim installs, is withdrawn or is abandoned; one whose
// delivery stopped stays until Try again, so its facts can still be shown.
type setupDelivery struct {
	since    time.Time
	attempts atomic.Int32
	nextAt   atomic.Int64 // unix milliseconds; 0 when nothing is scheduled
	running  atomic.Bool
}

func (d *setupDelivery) failed(next time.Time) {
	d.attempts.Add(1)
	d.nextAt.Store(next.UnixMilli())
}

// trackDelivery marks an approval being applied. The first approval's time is kept
// across retries, so "slow" counts from when the person approved.
func (h *ClaimHandler) trackDelivery(operationID string) *setupDelivery {
	fresh := &setupDelivery{since: time.Now()}
	current, _ := h.deliveries.LoadOrStore(operationID, fresh)
	d := current.(*setupDelivery)
	d.nextAt.Store(0)
	d.running.Store(true)
	return d
}

// SetupDelivery reports delivery for setupflow.Derive. A wait still open for the
// decision (after a restart, before Portico Services answers again) counts as running.
func (h *ClaimHandler) SetupDelivery(operationID string) (setupflow.Delivery, bool) {
	if h == nil {
		return setupflow.Delivery{}, false
	}
	if current, ok := h.deliveries.Load(operationID); ok {
		d := current.(*setupDelivery)
		out := setupflow.Delivery{Running: d.running.Load(), Since: d.since, Attempts: int(d.attempts.Load())}
		if next := d.nextAt.Load(); next != 0 && out.Running {
			out.NextRetryAt = time.UnixMilli(next)
		}
		return out, true
	}
	if current, ok := h.webWatches.Load(operationID); ok {
		select {
		case <-current.(*webClaimWatch).done:
		default:
			return setupflow.Delivery{Running: true}, true
		}
	}
	return setupflow.Delivery{}, false
}

// RetrySetupDelivery is Try again for an approval nothing is applying any more
// (setupflow S15): a finished wait for this claim is dropped and the recovery loop
// waits on Portico Services again from the saved note, under the server's own guard.
func (h *ClaimHandler) RetrySetupDelivery(operationID string) {
	if h == nil {
		return
	}
	if current, ok := h.webWatches.Load(operationID); ok {
		watch := current.(*webClaimWatch)
		select {
		case <-watch.done:
			h.webWatches.CompareAndDelete(operationID, watch)
		default:
			return // still trying
		}
	}
	h.wakeClaimRecovery()
}
