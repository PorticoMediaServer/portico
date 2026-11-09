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
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordUONoticeReclaimPlans = flag.Bool("record-uo-notice-reclaim-plans", false, "record UO notice reclaim native plans")

// The bounded reclaim statements are captured on the actual writer fixture.
// Every one is a keyed SEARCH on its declared index or primary key; Capture
// refuses any SCAN, temporary B-tree or unforced index. The golden is a
// comparison baseline, never registration authority.
func TestUONoticeReclaimPlans(t *testing.T) {
	w := wc008NativeExecutor(t)
	defs := uoNoticeReclaimDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	policy := w.config
	if _, err = sqliteconn.Assert(t.Context(), conn, policy); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	for _, name := range []string{"uo_notice_reclaim_statements.go", "uo_notice_reclaim.go", "uo_notice_reclaim_plans_test.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		hash.Write([]byte(name))
		hash.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		hash.Write(data)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	indexPolicy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: policy, SHA256: hex.EncodeToString(hash.Sum(nil)), Statistics: plan.Unanalyzed, Implementations: "UO notice reclaim component native fixture; bounded expiry/ACK/operation keysets only; no registration, authority or resource qualification"}, indexPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	// UPDATE/DELETE-only sets do not trigger Capture's INSERT-side FK audit.
	if g.ForeignKeys == nil {
		audit, err := cachedForeignKeys(t.Context(), conn, policy)
		if err != nil {
			t.Fatal(err)
		}
		g.ForeignKeys = &audit
	}
	if len(g.Manifest) != len(defs) || len(g.Plans) != len(defs) || g.ForeignKeys == nil || plan.CheckSchemaForeignKeys(g.ForeignKeys) != nil {
		t.Fatal("incomplete capture")
	}
	for _, statement := range g.Plans {
		for _, row := range statement.QueryPlan.Values {
			detail := row[3].(string)
			if strings.HasPrefix(detail, "SCAN ") || strings.Contains(detail, "TEMP B-TREE") {
				t.Fatalf("%s is not a keyed SEARCH: %s", statement.ID, detail)
			}
		}
	}
	fk, err := json.MarshalIndent(g.ForeignKeys, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	g.ForeignKeys = nil
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	jsonPath := filepath.Join("testdata", "uo-notice-reclaim-plans.json")
	fkPath := filepath.Join("testdata", "uo-notice-reclaim-plans-fk.json.gz")
	if *recordUONoticeReclaimPlans {
		if err = os.WriteFile(jsonPath, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err = gz.Write(fk); err != nil {
			t.Fatal(err)
		}
		if err = gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(fkPath, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(jsonPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = g.Compare(expected); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile(fkPath)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	expectedFK, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err = gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareExactJSON(expectedFK, fk); err != nil {
		t.Fatal(err)
	}
	t.Logf("%d exact reclaim IDs/plans; keyed SEARCH only; component-only", len(defs))
}
