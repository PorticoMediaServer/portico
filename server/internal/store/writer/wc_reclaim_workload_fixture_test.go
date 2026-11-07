package writer

import "testing"

// Test-only trusted retained claim composition, absent from product builds.
func WCReclaimWorkloadFixture(t *testing.T) (*Writer, *WCReclaimDriver) {
	t.Helper()
	return wcReclaimCompositionFixture(t)
}
