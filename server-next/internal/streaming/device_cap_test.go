package streaming

import (
	"errors"
	"testing"

	"github.com/PorticoMediaServer/portico/server-next/internal/decoder"
)

func TestHardwareConversionCapBelongsToEachPhysicalDevice(t *testing.T) {
	cfg := DefaultDeliveryConfiguration()
	cfg.MaxHardwareConversions = 1
	h := &HLS{settings: fixedSettings{cfg: cfg}}
	plan := func(id, backend string) *DeliveryPlan {
		return &DeliveryPlan{VideoAction: "convert", HardwareBackend: backend,
			ConversionProbe: &decoder.HardwareProbe{SelectedDeviceID: id}}
	}
	a, b := plan("card-a", "qsv"), plan("card-b", "qsv")
	s := &Streams{hls: h, byGrant: map[string]*stream{"first": {mode: "hls", state: streamLive, plan: a}}, live: map[string]*liveConversion{}}
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := s.admitLocked(b); err != nil {
		t.Fatal("the other physical device lost its own capacity", err)
	}
	if err := s.admitLocked(plan("card-a", "vaapi")); !errors.Is(err, ErrConversionCapacity) {
		t.Fatal("a second method bypassed the same device's cap", err)
	}
	s.live["second"] = &liveConversion{plan: b}
	if err := s.admitLocked(b); !errors.Is(err, ErrConversionCapacity) {
		t.Fatal("Live TV did not count on its device", err)
	}
	if err := s.admitLocked(&DeliveryPlan{VideoAction: "convert", HardwareBackend: "software"}); err != nil {
		t.Fatal("a device cap held a processor conversion", err)
	}
}
