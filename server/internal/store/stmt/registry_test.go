package stmt_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

func definition() stmt.Definition {
	return stmt.Definition{
		ID: "test.lookup", SQL: "SELECT ?, ?, ?", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Read,
		Params:  []stmt.Param{{Name: "id", Type: stmt.Integer}, {Name: "text", Type: stmt.Text, Nullable: true}, {Name: "bytes", Type: stmt.Blob}},
		Cohorts: []stmt.Cohort{{Name: "null-and-empty", Bindings: []any{int64(7), nil, []byte{}}}},
	}
}
func TestRegistryRejectsInvalidDefinitions(t *testing.T) {
	cases := map[string]func(*stmt.Definition){
		"empty ID":             func(d *stmt.Definition) { d.ID = "" },
		"missing owner":        func(d *stmt.Definition) { d.Owner = "" },
		"missing callsite":     func(d *stmt.Definition) { d.CallSites = nil },
		"empty callsite":       func(d *stmt.Definition) { d.CallSites = []string{""} },
		"invalid kind":         func(d *stmt.Definition) { d.Kind = "unknown" },
		"bad type":             func(d *stmt.Definition) { d.Params[0].Type = "json" },
		"duplicate name":       func(d *stmt.Definition) { d.Params[1].Name = "id" },
		"missing cohort":       func(d *stmt.Definition) { d.Cohorts = nil },
		"invalid cohort":       func(d *stmt.Definition) { d.Cohorts[0].Bindings[0] = "7" },
		"duplicate cohort":     func(d *stmt.Definition) { d.Cohorts = append(d.Cohorts, d.Cohorts[0]) },
		"empty SQL":            func(d *stmt.Definition) { d.SQL = "-- comment" },
		"multiple SQL":         func(d *stmt.Definition) { d.SQL += "; DELETE FROM items" },
		"missing placeholder":  func(d *stmt.Definition) { d.SQL = "SELECT ?, ?" },
		"numbered":             func(d *stmt.Definition) { d.SQL = "SELECT ?1, ?2, ?3" },
		"named":                func(d *stmt.Definition) { d.SQL = "SELECT :id, :text, :bytes" },
		"unterminated literal": func(d *stmt.Definition) { d.SQL = "SELECT 'broken" },
		"unterminated comment": func(d *stmt.Definition) { d.SQL = "SELECT ? /*" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			d := definition()
			change(&d)
			if _, err := stmt.New([]stmt.Definition{d}); err == nil {
				t.Fatal("invalid definition accepted")
			}
		})
	}
	d := definition()
	if _, err := stmt.New([]stmt.Definition{d, d}); err == nil {
		t.Fatal("duplicate ID accepted")
	}
}
func TestRegistryFreezesManifestAndBindings(t *testing.T) {
	d := definition()
	d.Cohorts[0].Bindings[2] = []byte{1}
	r, err := stmt.New([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(r.Manifest())
	d.SQL = "SELECT 'replaced'"
	d.Params[0].Type = stmt.Text
	d.CallSites[0] = "replaced"
	d.Cohorts[0].Bindings[2].([]byte)[0] = 9
	m := r.Manifest()
	m[0].Params[0].Name = "replaced"
	m[0].Cohorts[0].Bindings[2].([]byte)[0] = 8
	after, _ := json.Marshal(r.Manifest())
	if string(before) != string(after) {
		t.Fatal("manifest is mutable")
	}
	s, _ := r.Lookup("test.lookup")
	sdef, _ := s.Definition()
	sdef.Params[0].Type = stmt.Text
	bytes := []byte{4}
	b, err := s.Bind(int64(7), "", bytes)
	if err != nil {
		t.Fatal(err)
	}
	bytes[0] = 9
	db := database(t)
	var id int64
	var value string
	var blob []byte
	if err := b.QueryRowContext(context.Background(), db).Scan(&id, &value, &blob); err != nil {
		t.Fatal(err)
	}
	if id != 7 || value != "" || !reflect.DeepEqual(blob, []byte{4}) {
		t.Fatalf("bindings changed: %d %q %v", id, value, blob)
	}
	if _, err := r.Lookup("test.missing"); err == nil {
		t.Fatal("unknown ID accepted")
	}
	var zero stmt.Statement
	if _, err := zero.Bind(); err == nil {
		t.Fatal("zero statement accepted")
	}
	var bound stmt.Bound
	if _, err := bound.ExecContext(context.Background(), db); err == nil {
		t.Fatal("zero bound executed")
	}
	if err := bound.QueryRowContext(context.Background(), db).Scan(&id); err == nil {
		t.Fatal("zero bound queried")
	}
	var row stmt.Row
	if err := row.Scan(&id); err == nil {
		t.Fatal("zero row accepted")
	}
}
func TestRegistryNativeBindingsAndNulls(t *testing.T) {
	r, err := stmt.New([]stmt.Definition{definition()})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := r.Lookup("test.lookup")
	for _, args := range [][]any{{7, nil, []byte{}}, {int64(7), nil, []byte(nil)}, {nil, nil, []byte{}}, {int64(7), 3, []byte{}}, {int64(7), nil}, {int64(7), nil, []byte{}, "extra"}} {
		if _, err := s.Bind(args...); err == nil {
			t.Fatalf("invalid bindings accepted: %#v", args)
		}
	}
	b, err := s.Bind(int64(0), nil, []byte{})
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	var text sql.NullString
	var bytes any
	if err := b.QueryRowContext(context.Background(), database(t)).Scan(&id, &text, &bytes); err != nil {
		t.Fatal(err)
	}
	empty, isBlob := bytes.([]byte)
	if id != 0 || text.Valid || !isBlob || len(empty) != 0 {
		t.Fatalf("NULL/empty lost: %d %+v %#v", id, text, bytes)
	}
	d := stmt.Definition{ID: "types", SQL: "SELECT ?, ?", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "r", Type: stmt.Real}, {Name: "b", Type: stmt.Boolean}}, Cohorts: []stmt.Cohort{{Name: "finite", Bindings: []any{1.5, true}}}}
	r, err = stmt.New([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	s, _ = r.Lookup("types")
	for _, number := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if _, err := s.Bind(number, true); err == nil {
			t.Fatal("nonfinite REAL accepted")
		}
	}
}
func TestRegistrySQLQuotesAndComments(t *testing.T) {
	d := stmt.Definition{ID: "quoted", SQL: "SELECT '?' AS [?:;], 'it''s ; :safe', \"@?\", `?`, ? /* :ignore ? */; -- ?\n", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Read, Params: []stmt.Param{{Name: "value", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "typical", Bindings: []any{"bound"}}}}
	if _, err := stmt.New([]stmt.Definition{d}); err != nil {
		t.Fatal(err)
	}
}
func TestRegistryExecReturningAndContext(t *testing.T) {
	db := database(t)
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, "CREATE TABLE items(id INTEGER PRIMARY KEY,value TEXT)"); err != nil {
		t.Fatal(err)
	}
	defs := []stmt.Definition{
		{ID: "write", SQL: "INSERT INTO items(id,value) VALUES(?,?)", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Write, Params: []stmt.Param{{Name: "id", Type: stmt.Integer}, {Name: "value", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "typical", Bindings: []any{int64(1), "first"}}}},
		{ID: "returning", SQL: "UPDATE items SET value=? WHERE id=? RETURNING value", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Write, Params: []stmt.Param{{Name: "value", Type: stmt.Text}, {Name: "id", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "typical", Bindings: []any{"next", int64(1)}}}},
		definition(),
	}
	r, err := stmt.New(defs)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := r.Lookup("write")
	b, _ := s.Bind(int64(1), "first")
	result, err := b.ExecContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		t.Fatalf("RowsAffected: %d, %v", affected, err)
	}
	if _, err := b.ExecContext(ctx, db); err == nil {
		t.Fatal("constraint error lost")
	}
	s, _ = r.Lookup("returning")
	b, _ = s.Bind("next", int64(1))
	var value string
	if err := b.QueryRowContext(ctx, db).Scan(&value); err != nil || value != "next" {
		t.Fatalf("RETURNING: %q %v", value, err)
	}
	b, _ = s.Bind("unused", int64(99))
	if err := b.QueryRowContext(ctx, db).Scan(&value); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("no-row error: %v", err)
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	cancel()
	b, _ = s.Bind("cancelled", int64(1))
	if _, err := b.ExecContext(cancelCtx, db); !errors.Is(err, context.Canceled) {
		t.Fatalf("context error: %v", err)
	}
	s, _ = r.Lookup("test.lookup")
	b, _ = s.Bind(int64(1), nil, []byte{})
	if _, err := b.ExecContext(ctx, db); err == nil {
		t.Fatal("read executed without query")
	}
	rows, err := b.QueryContext(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("missing query result")
	}
	// The manifest order and hash bind the exact SQL bytes, including whitespace.
	manifest := r.Manifest()
	if manifest[0].ID != "returning" || len(manifest[0].SQLSHA256) != 64 {
		t.Fatalf("bad manifest: %+v", manifest)
	}
	d := definition()
	d.SQL += " "
	other, err := stmt.New([]stmt.Definition{d})
	if err != nil {
		t.Fatal(err)
	}
	if other.Manifest()[0].SQLSHA256 == manifest[1].SQLSHA256 {
		t.Fatal("SQL bytes not hashed")
	}
	if !strings.Contains(manifest[2].SQL, "INSERT") {
		t.Fatal("unexpected order")
	}
}
func database(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:?"+connectionPragmas)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { db.Close() })
	return db
}

func TestRegistryManifestHasExplicitEmptyBindings(t *testing.T) {
	r, err := stmt.New([]stmt.Definition{{ID: "no.parameters", SQL: "SELECT 1", Owner: "test", CallSites: []string{"registry_test.go"}, Kind: stmt.Read, Cohorts: []stmt.Cohort{{Name: "empty"}}}})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := json.Marshal(r.Manifest())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bytes), `"params":[]`) || !strings.Contains(string(bytes), `"bindings":[]`) {
		t.Fatalf("no-argument bindings must be explicit empty lists: %s", bytes)
	}
}
