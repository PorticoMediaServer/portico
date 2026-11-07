package read

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// Each mutation changes compiling production code; the unchanged independent
// SQL oracle must reject it. Hosted lane runners execute these serially.
func TestRSSavedCollectionProductionMutants(t *testing.T) {
	path := "rs_saved_collection_statements.go"
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Error(err)
		}
	}()
	for _, needle := range []string{" AND r.deleted=0", " AND r.kind='collection'"} {
		if strings.Count(string(original), needle) < 1 {
			t.Fatal("mutation target drift")
		}
		mutant := strings.Replace(string(original), needle, "", 1)
		if err = os.WriteFile(path, []byte(mutant), 0600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("go", "test", ".", "-run", "^TestRSSavedCollectionAuthorityOracle$", "-count=1")
		result, runErr := cmd.CombinedOutput()
		if runErr == nil || !strings.Contains(string(result), "--- FAIL: TestRSSavedCollectionAuthorityOracle") || strings.Contains(string(result), "[build failed]") {
			t.Fatalf("production mutant survived or failed compilation: %s %v\n%s", needle, runErr, result)
		}
	}
}
