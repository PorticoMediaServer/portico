package plan

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// mpQueuePoint returns the compiled queue octet point as a matching definition.
func mpQueuePoint(t *testing.T) (mpOctetPoint, stmt.Definition) {
	t.Helper()
	p, ok := mpOctetPointFor(mpQueueOctetsID)
	if !ok {
		t.Fatal("MP queue octet point is not registered")
	}
	return p, mpCloneDefinition(stmt.Definition{
		ID: p.id, SQL: p.sql, Kind: stmt.Read, Params: p.params,
		Plan: stmt.Plan{Hot: true, Indexes: p.indexes, AutoIndexes: p.autos, PrimaryKeys: p.primary},
	})
}

// mpCloneDefinition detaches every slice so a negative mutation cannot edit the
// compiled point it is compared against.
func mpCloneDefinition(base stmt.Definition) stmt.Definition {
	d := base
	d.Params = slices.Clone(base.Params)
	d.Plan.Indexes = slices.Clone(base.Plan.Indexes)
	d.Plan.PrimaryKeys = slices.Clone(base.Plan.PrimaryKeys)
	d.Plan.Seeks = slices.Clone(base.Plan.Seeks)
	d.Plan.Waivers = slices.Clone(base.Plan.Waivers)
	d.Plan.AllowedScans = slices.Clone(base.Plan.AllowedScans)
	d.Plan.AutoIndexes = make([]stmt.AutoIndexBinding, len(base.Plan.AutoIndexes))
	for i, auto := range base.Plan.AutoIndexes {
		auto.Columns = slices.Clone(auto.Columns)
		d.Plan.AutoIndexes[i] = auto
	}
	return d
}

func TestMPOctetDefinitionRejectsChangedShape(t *testing.T) {
	_, base := mpQueuePoint(t)
	if !mpOctetDefinition(base) {
		t.Fatal("canonical MP point rejected")
	}
	for name, mutate := range map[string]func(*stmt.Definition){
		"sql":      func(d *stmt.Definition) { d.SQL += " " },
		"kind":     func(d *stmt.Definition) { d.Kind = stmt.Write },
		"cold":     func(d *stmt.Definition) { d.Plan.Hot = false },
		"params":   func(d *stmt.Definition) { d.Params = d.Params[:1] },
		"nullable": func(d *stmt.Definition) { d.Params[0].Nullable = true },
		"indexes":  func(d *stmt.Definition) { d.Plan.Indexes = nil },
		"autos":    func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Origin = "u" },
		"primary":  func(d *stmt.Definition) { d.Plan.PrimaryKeys = []string{"e"} },
		"seeks":    func(d *stmt.Definition) { d.Plan.Seeks = []string{"id=?"} },
		"waivers":  func(d *stmt.Definition) { d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID}} },
		"scans":    func(d *stmt.Definition) { d.Plan.AllowedScans = []string{"x"} },
		"insert":   func(d *stmt.Definition) { d.Plan.NativeInsert = "x" },
		"retained": func(d *stmt.Definition) { d.Plan.NativeRetained = "x" },
		"read": func(d *stmt.Definition) {
			d.Plan.NativeRead = &stmt.FTSRead{Slot: stmt.SearchOwnPrefixReadV1, Shape: stmt.FTSPoint}
		},
	} {
		d := mpCloneDefinition(base)
		mutate(&d)
		if mpOctetDefinition(d) {
			t.Fatalf("%s: changed MP point definition accepted", name)
		}
	}
}

func TestMPOctetPointRequiresExactSearches(t *testing.T) {
	p, _ := mpQueuePoint(t)
	searches := p.searches
	rows := Rows{}
	for _, detail := range searches {
		rows.Values = append(rows.Values, []any{int64(2), int64(0), int64(0), detail})
	}
	if err := checkMPOctetPoint(p, rows); err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func(*Rows){
		"missing": func(r *Rows) { r.Values = r.Values[:len(r.Values)-1] },
		"scan": func(r *Rows) {
			r.Values[0][3] = "SCAN q"
		},
		"wrong-index": func(r *Rows) {
			r.Values[0][3] = "SEARCH q USING INDEX other (id=?)"
		},
		"short-row": func(r *Rows) { r.Values[0] = r.Values[0][:3] },
		"non-text":  func(r *Rows) { r.Values[0][3] = int64(1) },
	} {
		changed := rows
		changed.Values = append([][]any(nil), rows.Values...)
		mutate(&changed)
		if err := checkMPOctetPoint(p, changed); err == nil {
			t.Fatalf("%s: incomplete MP point EQP accepted", name)
		}
	}
}

func TestMPEntityAuditRequiresIntegerIdentity(t *testing.T) {
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(context.Background(), filepath.Join(t.TempDir(), "entity.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	point := mpOctetPoint{entity: true}
	for name, ddl := range map[string]string{
		"canonical": "CREATE TABLE catalog_entities(id INTEGER PRIMARY KEY, title TEXT)",
		"text":      "CREATE TABLE catalog_entities(id TEXT PRIMARY KEY, title TEXT)",
		"no-pk":     "CREATE TABLE catalog_entities(id INTEGER, title TEXT)",
	} {
		if _, err := conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS catalog_entities"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), ddl); err != nil {
			t.Fatal(err)
		}
		err := auditMPOctetPoint(context.Background(), conn, point)
		if name == "canonical" {
			if err != nil {
				t.Fatalf("canonical entity rejected: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: non-integer entity identity admitted", name)
		}
	}
}

func TestMPAutoindexAuditRejectsNativeKeyDrift(t *testing.T) {
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 2048, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(context.Background(), filepath.Join(t.TempDir(), "probe.db"), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	binding := stmt.AutoIndexBinding{Table: "mp_probe", Name: "sqlite_autoindex_mp_probe_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	point := mpOctetPoint{autos: []stmt.AutoIndexBinding{binding}}
	for name, ddl := range map[string]string{
		"canonical":  "CREATE TABLE mp_probe(id TEXT PRIMARY KEY NOT NULL)",
		"missing":    "CREATE TABLE mp_probe(id TEXT)",
		"unique":     "CREATE TABLE mp_probe(id TEXT UNIQUE NOT NULL)",
		"nocase":     "CREATE TABLE mp_probe(id TEXT PRIMARY KEY COLLATE NOCASE NOT NULL)",
		"descending": "CREATE TABLE mp_probe(id TEXT, PRIMARY KEY(id DESC))",
		"extra-key":  "CREATE TABLE mp_probe(id TEXT, extra TEXT, PRIMARY KEY(id,extra))",
	} {
		if _, err := conn.ExecContext(context.Background(), "DROP TABLE IF EXISTS mp_probe"); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(context.Background(), ddl); err != nil {
			t.Fatal(err)
		}
		err := auditMPOctetPoint(context.Background(), conn, point)
		if name == "canonical" {
			if err != nil {
				t.Fatalf("canonical probe rejected: %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("%s: native key drift admitted", name)
		}
	}
}
