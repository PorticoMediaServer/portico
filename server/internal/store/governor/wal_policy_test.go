package governor

import (
	"testing"
	"time"
)

func TestPhase1aWALPolicyIsTheAdoptedValue(t *testing.T) {
	p := Phase1aWALPolicy()
	if p.HardBytes != 512<<20 || p.ReserveBytes != 64<<20 || p.SoftDebtBytes != 64<<20 || p.ActivePressureBytes != 256<<20 ||
		p.MaxSampleAge != time.Second || !p.ConcurrentPassive || p.CheckpointMaxHold != 5*time.Second {
		t.Fatalf("adopted phase 1a WAL policy changed: %+v", p)
	}
	if _, err := NewWALGate(p); err != nil {
		t.Fatal("adopted policy is not a valid gate policy", err)
	}
	const largestForecast = 32 << 20 // largest closed command append forecast
	if p.ActivePressureBytes > p.HardBytes-p.ReserveBytes || p.SoftDebtBytes > p.ActivePressureBytes ||
		p.ReserveBytes < 2*largestForecast || p.ActivePressureBytes+largestForecast > p.HardBytes-p.ReserveBytes {
		t.Fatalf("adopted policy invariants broken: %+v", p)
	}
}
