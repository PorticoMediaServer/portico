package playback

import (
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/capabilityreport"
)

func hardwareStatus() (capabilityreport.Status, bool) {
	for _, s := range capabilityreport.All() {
		if s.Capability == capabilityreport.Hardware {
			return s, true
		}
	}
	return capabilityreport.Status{}, false
}

// Review P22: the hardware capability is reported when it changes, not per
// session; switching back to automatic clears a failure reported while a
// backend was chosen.
func TestHardwareCapabilityReportedOnChangeOnly(t *testing.T) {
	s := &Service{}
	s.noteHardware("vaapi", false, "device_missing")
	st, ok := hardwareStatus()
	if !ok || st.Available || st.Code != "hardware_unavailable" {
		t.Fatalf("chosen backend unusable: %+v", st)
	}
	first := s.hardwareSeen.Load()
	s.noteHardware("vaapi", false, "device_missing")
	if s.hardwareSeen.Load() != first {
		t.Fatal("an unchanged state was reported again")
	}
	s.noteHardware("auto", true, "")
	if st, _ = hardwareStatus(); !st.Available {
		t.Fatalf("automatic didn't clear the failure: %+v", st)
	}
	again := s.hardwareSeen.Load()
	s.noteHardware("", true, "")
	if s.hardwareSeen.Load() != again {
		t.Fatal("every usable state should read the same")
	}
}
