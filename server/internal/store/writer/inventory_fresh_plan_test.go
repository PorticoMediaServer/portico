package writer

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

var recordInventoryFreshPlan = flag.Bool("record-inventory-fresh-plan", false, "deliberately record the fresh-only library eligibility read")

func TestInventoryFreshLibraryPlanGolden(t *testing.T) {
	_, _, e, _ := inventoryFacadeFixture(t)
	conn, err := e.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var retired, before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT retired FROM catalog_libraries WHERE id=8 AND library_id='retired-library'").Scan(&retired); err != nil || retired != 1 {
		t.Fatal("retired cohort is not an actual retired row", retired, err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden := captureEffectiveFixture(t, conn, e.config, []stmt.Definition{inventoryFreshLibraryDefinition()},
		"inventory_dispatch_test.go", "inventory_resolution_test.go", "scan_receipts_test.go")
	if len(golden.Manifest) != 1 || len(golden.Plans) != 3 || golden.ForeignKeys != nil {
		t.Fatal("incomplete fresh-only read qualification")
	}
	for _, p := range golden.Plans {
		if p.ID != inventoryFreshLibrary || len(p.QueryPlan.Values) != 1 || len(p.Explain.Values) == 0 || p.Insert != nil {
			t.Fatal("fresh gate must remain one compiled indexed read", p.ID, p.Cohort)
		}
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || after != before {
		t.Fatal("Capture changed native fixture effects", before, after, err)
	}
	data, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "scan-inventory-fresh-library.json")
	if *recordInventoryFreshPlan {
		if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(append(data, '\n'), expected) {
		t.Fatal("fresh library plan differs from the recorded complete Capture")
	}
}
