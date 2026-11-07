package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordSearchHeadPlans = flag.Bool("record-search-head-plans", false, "deliberately record head-publish native plans")

func TestSearchHeadStatementPlans(t *testing.T) {
	w, _ := searchHeadNativeSeed(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defs := searchHeadDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	h := sha256.New()
	for _, name := range []string{"search_head_plan_test.go", "search_head_test.go", "search_head_transition_test.go", "search_head_statements.go", "search_fragment_stage_test.go", "search_fragment_transition_test.go", "search_image_stage_plan_test.go", "search_publish_history_test.go", "search_image_begin_command_test.go", "search_image_stage_statements.go", "search_publish_receipts.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		h.Write([]byte(name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		h.Write(data)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(h.Sum(nil)), Statistics: plan.Unanalyzed,
		Implementations: "FIRST sealed entity own-primary-title head; original own native replacement; indexed tail and exact source/accounting/held custody; FULL pre-BEGIN only; no governor, native Work or runtime eligibility qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed effects", before, after, err)
	}
	if len(defs) != 30 || len(g.Plans) != 45 || g.ForeignKeys == nil || plan.CheckSchemaForeignKeys(g.ForeignKeys) != nil {
		t.Fatal("incomplete 012/013 capture inventory", len(defs), len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	fk := g.ForeignKeys
	g.ForeignKeys = nil
	actual, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "search-head-publish.json")
	fkPath := filepath.Join("testdata", "search-head-publish-fk.json.gz")
	if *recordSearchHeadPlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(fkJSON); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(reader)
	closeErr := reader.Close()
	if err != nil || closeErr != nil {
		t.Fatal(err, closeErr)
	}
	if err := plan.CompareJSON(expected, fkJSON); err != nil {
		t.Fatal(err)
	}
	t.Log("30 IDs / 45 structural cohorts; 1 receipt INSERT + 2 closed own native operations; shared-schema FK audit; no accepting or resource qualification")
}
