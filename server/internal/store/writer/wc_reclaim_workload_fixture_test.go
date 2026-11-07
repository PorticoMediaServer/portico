package writer

import "testing"

// Test-only trusted retained claim composition, absent from product builds.
func WCReclaimWorkloadFixture(t *testing.T) (*Writer, *WCReclaimDriver) {
	t.Helper()
	w, _, d := wcReclaimCompositionProgressFixtureClock(t, true)
	return w, d
}
