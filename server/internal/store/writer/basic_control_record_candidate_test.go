package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// Temporary explicit hosted recorder; excluded from the three-file handoff.
func TestBasicControlRecordCandidate(t *testing.T) {
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("record-candidate requires bounded hosted evidence destination")
	}
	actual, fk := captureBasicControlPlans(t)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]string{}
	for name, b := range map[string][]byte{"scan-basic-control.json": actual, "scan-basic-control-fk.json.gz": fk} {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0644); err != nil {
			t.Fatal(err)
		}
		h := sha256.Sum256(b)
		manifest[name] = hex.EncodeToString(h[:])
	}
	b, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "basic-control-candidate-sha256.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}
