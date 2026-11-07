package writer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPBReclaimerProtocolCapture(t *testing.T) {
	old := *recordSearchReclaimerProtocol
	*recordSearchReclaimerProtocol = true
	defer func() { *recordSearchReclaimerProtocol = old }()
	TestSearchReclaimerProtocolStatementPlans(t)
	dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
	if dir == "" {
		t.Fatal("missing runner evidence directory")
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"search-reclaimer-protocol.json", "search-reclaimer-protocol-fk.json.gz"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
