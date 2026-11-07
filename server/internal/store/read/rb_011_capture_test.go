package read

import (
	"os"
	"path/filepath"
	"testing"
)

// Disposable runner capture adapter. This file is excluded from every rebuilt
// PR: those retain the reviewed code and adopt only the resulting golden.
func rb011Export(t *testing.T, name string) {
	t.Helper()
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("standard-runner evidence directory required")
	}
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, name), data, 0644); err != nil {
		t.Fatal(err)
	}
}
func TestRB011CaptureScalar(t *testing.T) {
	*recordRBScalarPlans = true
	defer func() { *recordRBScalarPlans = false }()
	TestRBScalarPlans(t)
	rb011Export(t, "rb-scalar-plans.json.gz")
}
func TestRB011CaptureChronology(t *testing.T) {
	*recordRBChronologyPlans = true
	defer func() { *recordRBChronologyPlans = false }()
	TestRBChronologyPlans(t)
	rb011Export(t, "rb-chronology-plans.json.gz")
}
func TestRB011CaptureRandom(t *testing.T) {
	*recordRBRandomPlans = true
	defer func() { *recordRBRandomPlans = false }()
	TestRBRandomPlans(t)
	rb011Export(t, "rb-random-plans.json.gz")
}
func TestRB011CaptureFacet(t *testing.T) {
	// This original test exports the new capture before comparing its old
	// baseline. A schema-only mismatch is expected until reviewed adoption.
	TestRBExactFacetPlans(t)
}
