package streaming

import (
	"context"
	"errors"
	"syscall"
)

func (h *HLS) publicationFailed(id string, owned *hlsWindow, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if w := h.windows[id]; w == owned {
		w.publicationError = err
	}
}

// A changed initialization retires the entire stream: no old address may see a
// differently shaped playlist. The same conversion carries on in software as a
// new stream with a new address, and the session layer is told through the
// stream's Replaced function. Without one, the stream fails with its reason.
func (h *HLS) fragmentedFailure(ctx context.Context, id string, plan *DeliveryPlan, position float64, cause error) {
	if errors.Is(cause, syscall.ENOSPC) && h.retryMemoryOutput(id) {
		return
	}
	if !errors.Is(cause, errMP4InitChanged) || plan == nil {
		h.failedWith(ctx, id, FailureConverter, "fragmented output could not be published")
		return
	}
	next, e := softwarePlan(plan)
	if e != nil || h.replace == nil || !h.replace(id, next, position) {
		h.failedWith(ctx, id, FailureConverter, "fragmented output could not be converted")
	}
}
