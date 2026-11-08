package writer

import "testing"

func TestCanonicalBasicStartGateRefusesWithoutThePressureHook(t *testing.T) {
	for name, c := range map[string]*canonicalBasicComponent{
		"nil component":     nil,
		"no writer":         {},
		"no maintenance":    {writer: &Writer{effective: &effectiveRuntime{}}},
		"no keeper owner":   {writer: &Writer{effective: &effectiveRuntime{maintenance: &checkpointMaintenance{}}}},
		"hook but no owner": {maintenance: &canonicalN1MaintenanceOwner{}, writer: &Writer{effective: &effectiveRuntime{}}},
	} {
		if err := canonicalBasicSustainedGate(c); err == nil {
			t.Fatalf("%s: the Basic start gate must refuse without the keeper and pressure hook", name)
		}
	}
}
