package writer

import "testing"

// Test-only trusted birth/composition; absent from product builds.
func WCSecurityWorkloadFixture(t *testing.T) (*Writer, *WCSecurityDriver, string) {
	t.Helper()
	return wcSecurityCompositionFixture(t, "owner", "member")
}
