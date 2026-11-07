package plan_test

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"flag"
	"io"
	"os"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordSchemaEvidence = flag.Bool("record-schema-evidence", false, "record the one adopted-schema metadata and FK baseline")

func TestPlanSharedSchemaEvidence(t *testing.T) {
	conn, _, f, _ := fixture(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	e, err := plan.CaptureSchemaEvidence(t.Context(), conn, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/schema-evidence.json.gz"
	if *recordSchemaEvidence {
		var out bytes.Buffer
		z := gzip.NewWriter(&out)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, out.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	defer z.Close()
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareExactJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	t.Logf("one shared embedded-schema evidence: %d native tables, %d FK edges / %d probes", len(e.Tables), len(e.ForeignKeys), 3*len(e.ForeignKeys))
}
