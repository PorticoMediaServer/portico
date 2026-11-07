package writer

import (
	"os"
	"path/filepath"
	"testing"
)

// Disposable hosted operator only; never enters the product PR.
func TestSCBasicPhase011Capture(t *testing.T) {
	*recordBasicStreamPlans = true
	t.Run("stream", TestBasicStreamStatementPlans)
	*recordBasicControlPlans = true
	t.Run("control", TestBasicControlStatementPlans)
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("hosted evidence directory required")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"scan-basic-stream-stage.json", "scan-basic-stream-stage-fk.json.gz", "scan-basic-control.json", "scan-basic-control-fk.json.gz"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
