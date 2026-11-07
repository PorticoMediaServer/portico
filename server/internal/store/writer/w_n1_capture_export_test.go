package writer

import (
	"os"
	"path/filepath"
	"testing"
)

// Temporary hosted candidate only: export the deliberate real Capture outputs
// for owner comparison. Never included in a product PR or qualification claim.
func TestWN1CaptureExport(t *testing.T) {
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Skip("hosted evidence export only")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	oldFresh, oldHistory := *recordInventoryFreshPlan, *recordScanLifecycleHistoryPlans
	defer func() { *recordInventoryFreshPlan, *recordScanLifecycleHistoryPlans = oldFresh, oldHistory }()
	*recordInventoryFreshPlan, *recordScanLifecycleHistoryPlans = true, true
	t.Run("fresh", TestInventoryFreshLibraryPlanGolden)
	t.Run("history", TestScanLifecycleHistoryStatementPlans)
	for _, name := range []string{"scan-inventory-fresh-library.json", "scan-run-lifecycle-history.json"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
