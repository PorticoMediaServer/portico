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
	// Retain the existing complete issuer fixture's native plans as source
	// facts, including its actual populated source/policy/logical-root points.
	// This remains setup Capture, not a Work or physical qualification result.
	f := newScanDriverFixture(t, scanIssuerStatementFixture)
	conn, err := f.driver.opening.executor.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	golden := captureEffectiveFixture(t, conn, f.driver.opening.executor.config,
		scanLifecycleCommandDefinitions(), "scan_driver_test.go", "scan_run_lifecycle_test.go")
	data, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "scan-issuer-current-definitions.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
}
