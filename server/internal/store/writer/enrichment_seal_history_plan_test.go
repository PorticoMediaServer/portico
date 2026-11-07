package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestSealHistoryStatementPlans(t *testing.T) {
	// Complete actual component population only. No source authority, profile,
	// live gate or Work/HOLD/WAL qualification is produced by a static Capture.
	w, _, _ := sealHistoryNativeReceipt(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := enrichmentSealHistoryCommandDefinitions()
	for _, d := range enrichmentSealProofLengthDefinitions() {
		if d.ID == sealEvidenceHeaderLengths {
			defs = append(defs, d)
		}
	}
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexes, err := plan.ReadIndexPolicy(raw)
	if err != nil {
		t.Fatal(err)
	}
	fixtureBytes, err := os.ReadFile("enrichment_seal_history_test.go")
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	io.WriteString(h, enrichmentStageFixtureIdentity(t))
	h.Write(fixtureBytes)
	policy := w.config
	policy.Synchronous = sqliteconn.Normal
	var before, after int64
	if err = conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	golden, err := plan.Capture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "current canonical; bounded retained-seal history/dispatcher and byte-point component; no fresh producer/proof or physical qualification"}, indexes)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("capture changed component population", before, after, err)
	}
	if len(golden.Manifest) != 9 || len(golden.Plans) != 12 {
		t.Fatal("incomplete closed history capture", len(golden.Manifest), len(golden.Plans))
	}
	data, err := golden.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "enrichment-seal-history.json.gz")
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		// First capture is a PROPOSAL artifact only. The owner reviews/commits it;
		// no code path auto-adopts bytes from a runner or rewrites the baseline.
		dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR")
		if dir == "" {
			t.Fatal("missing reviewed history baseline; collect bounded hosted proposal first")
		}
		if err = os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		var b bytes.Buffer
		z := gzip.NewWriter(&b)
		if _, err = z.Write(append(data, '\n')); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(dir, "enrichment-seal-history.proposed.json.gz"), b.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		t.Log("proposed capture only: no reviewed baseline comparison yet")
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	z, err := gzip.NewReader(file)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	err = errors.Join(err, z.Close())
	if err != nil {
		t.Fatal(err)
	}
	if err = golden.Compare(expected); err != nil {
		t.Fatal(err)
	}
}
