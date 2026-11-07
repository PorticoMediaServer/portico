package writer

import "testing"

// WCProgressWorkloadFixture exposes this test-only trusted composition to an
// external integration test. It is absent from every product build and supplies
// no source, SQL, custody, floor or clock override to workload callers.
func WCProgressWorkloadFixture(t *testing.T) (*Writer, *WCProgressDriver) {
	t.Helper()
	w, d, _ := wcProgressCompositionFixtureClock(t, true)
	return w, d
}
