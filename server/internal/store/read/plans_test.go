package read

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordBrowsePlans = flag.Bool("record-browse-plans", false, "deliberately record reviewed native browse plans")

func TestBrowsePlans(t *testing.T) {
	f := newBrowseFixture(t)
	seed, err := os.ReadFile("browse_test.go")
	if err != nil {
		t.Fatal(err)
	}
	start, end := bytes.Index(seed, []byte("func seedBrowse(")), bytes.Index(seed, []byte("func (f *browseFixture) window("))
	if start < 0 || end <= start {
		t.Fatal("missing fixture journal")
	}
	fixtureHash := fmt.Sprintf("%x", sha256.Sum256(seed[start:end]))
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := Registry()
	if err != nil {
		t.Fatal(err)
	}
	if len(registry.Manifest()) != 15 {
		t.Fatal("review changed statement inventory")
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 32}}
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	type capture struct {
		Profile string
		Golden  plan.Golden
	}
	var captures []capture
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			f.exec(t, "ANALYZE")
		}
		golden, err := plan.Capture(t.Context(), conn, registry, plan.Fixture{ConnectionPolicy: cfg, SHA256: fixtureHash, Statistics: stats, Implementations: "canonical " + schema.SHA256 + "; bundled 3.53.4 builtin functions/collations; seedBrowse source journal"}, policy)
		if err != nil {
			t.Fatal(err)
		}
		captures = append(captures, capture{Profile: string(stats), Golden: golden})
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	const path = "testdata/browse-plans.json.gz"
	if *recordBrowsePlans {
		var compressed bytes.Buffer
		z := gzip.NewWriter(&compressed)
		if _, err = z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err = z.Close(); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	z, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(z)
	if err != nil {
		t.Fatal(err)
	}
	if err = z.Close(); err != nil {
		t.Fatal(err)
	}
	if err = plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
	// Negative plan controls use native EQP and the real definition; each
	// mutation must lose its seek/forced index or acquire a forbidden sort.
	defs := registry.Manifest()
	for _, e := range defs {
		if !strings.Contains(string(e.ID), "title.ignore.asc.tail") {
			continue
		}
		for name, query := range map[string]string{
			"unforced":        strings.Replace(e.SQL, " INDEXED BY catalog_entities_title", "", 1),
			"computed-sort":   strings.Replace(e.SQL, "ORDER BY sort_key COLLATE NOCASE ASC", "ORDER BY lower(sort_key) ASC", 1),
			"wrong-collation": strings.ReplaceAll(e.SQL, "COLLATE NOCASE", "COLLATE BINARY"),
		} {
			t.Run(name, func(t *testing.T) {
				d := e.Definition
				d.SQL = query
				eqp := readEQP(t, conn, d)
				if err := plan.Check(d, eqp); err == nil {
					t.Fatal("bad plan accepted")
				}
			})
		}
	}
}

func readEQP(t *testing.T, conn *sql.Conn, d stmt.Definition) plan.Rows {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := plan.Rows{}
	for rows.Next() {
		var a, b, c int64
		var detail string
		if err := rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		out.Values = append(out.Values, []any{a, b, c, detail})
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
