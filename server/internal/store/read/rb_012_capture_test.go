package read

import (
	"os"
	"path/filepath"
	"testing"
)

// Disposable capture adapter. Reviewed implementations and final PRs omit it.
func TestRB012ScalarCapture(t *testing.T) {
	prior := *recordRBScalarPlans
	*recordRBScalarPlans = true
	defer func() { *recordRBScalarPlans = prior }()
	TestRBScalarPlans(t)
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("capture evidence directory is required")
	}
	data, e := os.ReadFile("testdata/rb-scalar-plans.json.gz")
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(dir, 0755); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(dir, "rb-scalar-plans.json.gz"), data, 0644); e != nil {
		t.Fatal(e)
	}
}
