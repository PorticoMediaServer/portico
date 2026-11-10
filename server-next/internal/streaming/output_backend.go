package streaming

import (
	"context"
	"strconv"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

// A session's hardware qualification belongs to the binary that earned it.
// Missing/stale codec, depth or rate-control qualification selects software
// before launching, without spending a false hardware failure on the circuit.
func (h *HLS) selectOutputBackend(ctx context.Context, id string, p *DeliveryPlan) {
	if p == nil || p.ConversionTarget == nil || p.VideoAction != "convert" || p.HardwareBackend == "" || p.HardwareBackend == "software" {
		return
	}
	t, probe := p.ConversionTarget, p.ConversionProbe
	key := t.VideoCodec + ":" + strconv.Itoa(t.BitDepth)
	facts := decoder.CurrentToolchain()
	valid := probe != nil && probe.Available && string(probe.Backend) == p.HardwareBackend && probe.OutputEncoders[key]
	if facts != nil {
		valid = valid && probe != nil && probe.Identity == facts.Identity
	}
	if t.RateControl == "quality" {
		valid = valid && t.VideoBitrateBPS == 0 && probe != nil && probe.OutputQuality[key]
	}
	if valid {
		return
	}
	p.HardwareBackend, p.HardwareNative, p.HardwareDevice = "software", false, ""
	h.mu.Lock()
	if s := h.streams[id]; s != nil && s.plan != nil {
		next := *s.plan
		next.HardwareBackend, next.HardwareNative, next.HardwareDevice = "software", false, ""
		s.plan = &next
	}
	h.mu.Unlock()
}
