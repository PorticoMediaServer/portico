package writer

import (
	"os"
	"path/filepath"
	"testing"
)

// Isolated recording adapter: not part of the domain PR or its fixture closure.
func TestPBReclaim012013Capture(t *testing.T) {
	old := *recordSearchReclaimPlans
	*recordSearchReclaimPlans = true
	defer func() { *recordSearchReclaimPlans = old }()
	TestSearchReclaimStatementPlans(t)
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("missing bounded runner evidence directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"search-fragment-reclaim.json", "search-fragment-reclaim-fk.json.gz"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
