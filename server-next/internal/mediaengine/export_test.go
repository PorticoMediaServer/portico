package mediaengine

import (
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
	"github.com/PorticoMediaServer/portico/server-next/internal/streaming"
)

// PlannedDelivery is the media engine's own plan behind a session's plan, so the media tests
// can hold what comes out of a stream against what was planned for it.
func PlannedDelivery(p playback.Plan) (streaming.DeliveryPlan, bool) {
	detail, ok := p.Detail.(*planned)
	if !ok {
		return streaming.DeliveryPlan{}, false
	}
	return detail.plan, true
}
