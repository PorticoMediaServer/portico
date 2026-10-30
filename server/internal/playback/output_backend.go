package playback

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"strconv"
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
	_, _ = dbwork.ExecWrite(ctx, h.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_delivery_plans SET hardware_backend='software',hardware_native=0,hardware_device='' WHERE session_id=?`, id)
}
