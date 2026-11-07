package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLaneInventoryBuildSelectedFilesAndFilters(t *testing.T) {
	dir := t.TempDir()
	source := `package p
import "testing"
// func TestComment(t *testing.T) {}
func TestMain(m *testing.M) {}
func TestAlpha(
 t *testing.T,
) {}
func TestBeta(t *testing.T) {}
func Example_alpha() { // Output: yes
}
func Example_documentationOnly() {}
`
	if err := os.WriteFile(filepath.Join(dir, "selected_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	// An excluded build-platform file must never be scanned.
	if err := os.WriteFile(filepath.Join(dir, "excluded_windows_test.go"), []byte("invalid Go"), 0600); err != nil {
		t.Fatal(err)
	}
	p := lanePackage{ImportPath: "p", Dir: dir, TestGoFiles: []string{"selected_test.go"}}
	got, err := inventory(inventoryInput{Packages: []lanePackage{p}, Run: "^(TestAlpha|Example_alpha)$"})
	if err != nil || len(got) != 2 || got[0].Name != "Example_alpha" || got[1].Name != "TestAlpha" {
		t.Fatalf("%v %v", got, err)
	}
	got, err = inventory(inventoryInput{Packages: []lanePackage{p}, Run: "TestAlpha/sub"})
	if err != nil || len(got) != 3 {
		t.Fatalf("slash candidates: %v %v", got, err)
	}
	got, err = inventory(inventoryInput{Packages: []lanePackage{p}, Run: ".", Tests: []string{"TestBeta"}})
	if err != nil || len(got) != 1 || got[0].Name != "TestBeta" {
		t.Fatalf("exact: %v %v", got, err)
	}
	if _, err = inventory(inventoryInput{Packages: []lanePackage{p}, Run: "["}); err == nil {
		t.Fatal("invalid RE2 accepted")
	}
	if _, err = inventory(inventoryInput{Packages: []lanePackage{p}, Run: "TestMissing"}); err == nil {
		t.Fatal("empty filter accepted")
	}
	if _, err = inventory(inventoryInput{Packages: []lanePackage{p}, Run: ".", Tests: []string{"TestMissing"}}); err == nil {
		t.Fatal("missing exact test accepted")
	}
}
