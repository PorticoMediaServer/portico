package plan_test

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordFTSReads = flag.Bool("record-fts-read-plans", false, "deliberately record the fifteen literal v1 native read plans")
var ftsReadSlots = []stmt.FTSReadSlot{stmt.SearchOwnPrefixReadV1, stmt.SearchFragmentPrefixReadV1, stmt.SearchFragmentLexicalReadV1, stmt.SearchFragmentGramsReadV1, stmt.CatalogPeopleNamesReadV1}

func ftsReadDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, slot := range ftsReadSlots {
		for _, shape := range []stmt.FTSReadShape{stmt.FTSFirst, stmt.FTSNext, stmt.FTSPoint} {
			descriptor := stmt.FTSRead{Slot: slot, Shape: shape}
			spec, _ := descriptor.Specification()
			d := stmt.Definition{ID: stmt.ID("fts.read." + spec.Table + "." + string(shape)), Owner: "native-read-contract", CallSites: []string{"fts_read_test.go"}, Kind: stmt.Read, SQL: spec.SQL, Params: []stmt.Param{{Name: "query", Type: stmt.Text}}, Plan: stmt.Plan{Hot: true, NativeRead: &descriptor}}
			switch shape {
			case stmt.FTSFirst:
				d.Params = append(d.Params, stmt.Param{Name: "limit", Type: stmt.Integer})
				d.Cohorts = []stmt.Cohort{{Name: "hit", Bindings: []any{"keanu", int64(2)}}, {Name: "empty", Bindings: []any{"absent", int64(2)}}, {Name: "max_window", Bindings: []any{"keanu", int64(64)}}}
			case stmt.FTSNext:
				d.Params = append(d.Params, stmt.Param{Name: "after_rowid", Type: stmt.Integer}, stmt.Param{Name: "limit", Type: stmt.Integer})
				d.Cohorts = []stmt.Cohort{{Name: "hit", Bindings: []any{"keanu", int64(1), int64(2)}}, {Name: "empty", Bindings: []any{"absent", int64(1), int64(2)}}, {Name: "max_window", Bindings: []any{"keanu", int64(0), int64(64)}}}
			case stmt.FTSPoint:
				d.Params = append(d.Params, stmt.Param{Name: "rowid", Type: stmt.Integer})
				d.Cohorts = []stmt.Cohort{{Name: "hit", Bindings: []any{"keanu", int64(1)}}, {Name: "empty", Bindings: []any{"keanu", int64(2)}}, {Name: "second_point", Bindings: []any{"keanu", int64(3)}}}
			}
			defs = append(defs, d)
		}
	}
	return defs
}

// Raw native postings only. These intentionally do not manufacture published
// content, producer authority, logical populations, ranks or visibility proof.
func ftsReadDatabase(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fts.db")
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO search_own_prefix(rowid,title_prefix_tokens,initial_prefix_tokens) VALUES(1,'keanu',''),(3,'keanu','')`,
		`INSERT INTO search_fragment_prefix(rowid,prefix_tokens) VALUES(1,'keanu'),(3,'keanu')`,
		`INSERT INTO search_fragment_lexical(rowid,lexical_tokens) VALUES(1,'keanu'),(3,'keanu')`,
		`INSERT INTO search_fragment_grams(rowid,normalized) VALUES(1,'keanu'),(3,'keanu')`,
		`INSERT INTO catalog_people_names(rowid,name) VALUES(1,'keanu'),(3,'keanu')`,
	} {
		if _, err := conn.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func ftsReader(t *testing.T, path string) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 65536, Columns: 128}}
	db, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	settings, err := sqliteconn.Assert(t.Context(), conn, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual Reader boundary %+v", settings)
	bytes, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(bytes)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(schema.SHA256 + " raw native postings1,3/keanu/all5; no external-content population"))
	return conn, plan.Fixture{ConnectionPolicy: cfg, SHA256: fmt.Sprintf("%x", sum), Statistics: plan.Unanalyzed, Implementations: "builtin fts5 fixed v1 formats; raw postings only, not logical sources, bank eligibility or authority"}, policy
}

func TestFTSReadGoldenAndPhysicalMapping(t *testing.T) {
	path := ftsReadDatabase(t)
	one, f, policy := ftsReader(t, path)
	two, _, _ := ftsReader(t, path)
	defs := ftsReadDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), one, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	other, err := plan.Capture(t.Context(), two, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	first := defs[0]
	rawOne := ftsRawVM(t, one, first)
	rawTwo := ftsRawVM(t, two, first)
	if len(rawOne) != len(rawTwo) {
		t.Fatal("physical VM length drift")
	}
	pointers := 0
	for i, row := range rawOne {
		for j, value := range row {
			if row[1] == "VOpen" && j == 5 {
				if value == rawTwo[i][j] {
					t.Fatal("control requires distinct physical VOpen pointers")
				}
				pointers++
				continue
			}
			if !reflect.DeepEqual(value, rawTwo[i][j]) {
				t.Fatal("raw physical operand drift outside VOpen P4", i, j)
			}
		}
	}
	if pointers != 1 {
		t.Fatal("native VOpen inventory", pointers)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	if err := other.Compare(actual); err != nil {
		t.Fatal("physical connections differ after ONLY approved pointer mapping", err)
	}
	registry, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 15 || len(g.Plans) != 45 || g.ForeignKeys != nil {
		t.Fatal("closed native read inventory")
	}
	for _, d := range defs {
		statement, err := registry.Lookup(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, cohort := range d.Cohorts {
			bound, err := statement.Bind(cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			rows, err := bound.QueryContext(t.Context(), one)
			if err != nil {
				t.Fatal(err)
			}
			var ids []int64
			for rows.Next() {
				var id int64
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
			var want []int64
			if cohort.Name != "empty" {
				switch d.Plan.NativeRead.Shape {
				case stmt.FTSPoint:
					want = []int64{cohort.Bindings[1].(int64)}
				case stmt.FTSNext:
					if cohort.Name == "hit" {
						want = []int64{3}
					} else {
						want = []int64{1, 3}
					}
				default:
					want = []int64{1, 3}
				}
			}
			if !reflect.DeepEqual(ids, want) {
				t.Fatal(d.ID, cohort.Name, ids, want)
			}
		}
		for _, query := range []string{"", "bad\x00query", string([]byte{0xff}), strings.Repeat("a", 1540)} {
			args := append([]any{}, d.Cohorts[0].Bindings...)
			args[0] = query
			if _, err := statement.Bind(args...); err == nil {
				t.Fatal("invalid/unbounded native MATCH payload bound", d.ID)
			}
		}
		if d.Plan.NativeRead.Shape != stmt.FTSPoint {
			spec, _ := d.Plan.NativeRead.Specification()
			for _, limit := range []int64{-1, 0, 65} {
				args := append([]any{}, d.Cohorts[0].Bindings...)
				args[spec.LimitIndex] = limit
				if _, err := statement.Bind(args...); err == nil {
					t.Fatal("unbounded/over-window LIMIT bound", d.ID, limit)
				}
			}
		}
	}
	filename := filepath.Join("testdata", "fts-read.json.gz")
	if *recordFTSReads {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filename)
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
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}

func TestFTSReadClosedDefinitionsAndSources(t *testing.T) {
	path := ftsReadDatabase(t)
	conn, f, policy := ftsReader(t, path)
	original := ftsReadDefinitions()[0]
	for _, tc := range []struct {
		name  string
		alter func(*stmt.Definition)
	}{
		{"unknown_slot", func(d *stmt.Definition) {
			d.Plan.NativeRead = &stmt.FTSRead{Slot: "unknown.v1.read", Shape: stmt.FTSFirst}
		}},
		{"unknown_shape", func(d *stmt.Definition) {
			d.Plan.NativeRead = &stmt.FTSRead{Slot: stmt.SearchOwnPrefixReadV1, Shape: "all"}
		}},
		{"content_projection", func(d *stmt.Definition) {
			d.SQL = "SELECT rowid,rank FROM search_own_prefix WHERE search_own_prefix MATCH ? ORDER BY rowid ASC LIMIT ?"
		}},
		{"descending", func(d *stmt.Definition) {
			d.SQL = "SELECT rowid FROM search_own_prefix WHERE search_own_prefix MATCH ? ORDER BY rowid DESC LIMIT ?"
		}},
		{"write_kind", func(d *stmt.Definition) { d.Kind = stmt.Write }},
		{"nullable_limit", func(d *stmt.Definition) { d.Params = append([]stmt.Param{}, d.Params...); d.Params[1].Nullable = true }},
		{"waiver", func(d *stmt.Definition) {
			d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "eqp", Form: "SCAN", Match: "SCAN search_own_prefix", Reason: "not allowed"}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := original
			tc.alter(&d)
			if _, err := stmt.Describe([]stmt.Definition{d}); err == nil {
				t.Fatal("non-closed read description accepted")
			}
		})
	}
	// No descriptor means no virtual-source certification, even for the same SQL.
	plain := original
	plain.Plan.NativeRead = nil
	catalogue, err := stmt.Describe([]stmt.Definition{plain})
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err == nil {
		t.Fatal("generic virtual source accepted")
	}
	if _, err := stmt.New([]stmt.Definition{plain}, g.Receipt()); err == nil {
		t.Fatal("refused virtual source authorized execution")
	}
	// Descriptors remain detached values in the inert catalogue/manifest.
	d := ftsReadDefinitions()[0]
	catalogue, err = stmt.Describe([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	d.Plan.NativeRead.Shape = stmt.FTSPoint
	if catalogue.Manifest()[0].Plan.NativeRead.Shape != stmt.FTSFirst {
		t.Fatal("caller mutated captured descriptor")
	}
	manifest := catalogue.Manifest()
	manifest[0].Plan.NativeRead.Slot = "unknown"
	if catalogue.Manifest()[0].Plan.NativeRead.Slot != stmt.SearchOwnPrefixReadV1 {
		t.Fatal("manifest mutated catalogue")
	}
	// A changed external-content bank relation cannot inherit an existing slot.
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	writer, _, err := sqliteconn.Open(t.Context(), path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	writerConn, err := writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	writerFixture := f
	writerFixture.ConnectionPolicy = cfg
	if _, err := plan.Capture(t.Context(), writerConn, catalogue, writerFixture, policy); err == nil || !strings.Contains(err.Error(), "Reader custody") {
		t.Fatal("Writer-policy native read qualification", err)
	}
	if err := writerConn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.ExecContext(t.Context(), "DROP VIEW search_fragment_content; CREATE VIEW search_fragment_content AS SELECT * FROM search_fragments WHERE 1=0"); err != nil {
		t.Fatal(err)
	}
	frag := ftsReadDefinitions()[3]
	catalogue, err = stmt.Describe([]stmt.Definition{frag})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Capture(t.Context(), conn, catalogue, f, policy); err == nil {
		t.Fatal("changed content relation certified")
	}
}

func ftsRawVM(t *testing.T, conn *sql.Conn, d stmt.Definition) [][]any {
	t.Helper()
	rows, err := conn.QueryContext(t.Context(), "EXPLAIN "+d.SQL, d.Cohorts[0].Bindings...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result [][]any
	for rows.Next() {
		row := make([]any, 8)
		dest := make([]any, 8)
		for i := range row {
			dest[i] = &row[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
