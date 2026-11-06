package plan_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func nativeFTSDefinitions() []stmt.Definition {
	defs := []stmt.Definition{}
	for _, slot := range []stmt.FTSInsertSlot{stmt.SearchFragmentPrefixV1, stmt.SearchFragmentLexicalV1, stmt.SearchFragmentGramsV1} {
		spec, _ := slot.Specification()
		defs = append(defs, stmt.Definition{ID: stmt.ID(slot), SQL: spec.SQL, Kind: stmt.Write, Owner: "plan.fts.test", CallSites: []string{"fts_insert_test.go"},
			Params:  []stmt.Param{{Name: "rowid", Type: stmt.Integer}, {Name: spec.Column, Type: stmt.Text}},
			Cohorts: []stmt.Cohort{{Name: "nonempty", Bindings: []any{int64(41), "000061"}}, {Name: "empty", Bindings: []any{int64(42), ""}}},
			Plan:    stmt.Plan{Hot: true, NativeInsert: slot}})
	}
	return defs
}

func TestFTSInsertCaptureClosedSlotsAndRuntimeBounds(t *testing.T) {
	conn, _, f, _ := fixture(t)
	if _, err := conn.ExecContext(t.Context(), "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(t.Context(), conn); err != nil {
		t.Fatal(err)
	}
	f.SHA256 = schema.SHA256
	f.Implementations = "fixed three v1 FTS5 INSERT slots; empty adopted schema; EXPLAIN only"
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	defs := nativeFTSDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalogue.Lookup(defs[0].ID); !errors.Is(err, stmt.ErrUnqualified) {
		t.Fatal("descriptor minted authority", err)
	}
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), conn, catalogue, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture executed FTS effects", before, after, err)
	}
	if len(g.Plans) != 6 || g.ForeignKeys == nil {
		t.Fatal("missing complete Capture/native/FK evidence")
	}
	for _, p := range g.Plans {
		if p.NativeInsert == nil || p.Insert != nil || len(p.Explain.Values) != 13 || len(p.QueryPlan.Values) != 0 ||
			len(p.NativeInsert.Columns.Values) != 3 || len(p.NativeInsert.ShadowObjects.Values) != 4 || len(p.NativeInsert.ShadowColumns) != 4 {
			t.Fatal("incomplete or fabricated native destination audit", p.ID)
		}
		if p.Explain.Values[6][5] != p.NativeInsert.Identity || p.Explain.Values[9][5] != p.NativeInsert.Identity {
			t.Fatal("mapped identity differs from audited destination")
		}
		data, _ := json.Marshal(p.Explain)
		if bytes.Contains(data, []byte("vtab:")) {
			t.Fatal("unmapped native address survived")
		}
	}
	r, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	plain := defs[0]
	plain.Plan.NativeInsert = ""
	ordinary, err := stmt.Describe([]stmt.Definition{plain})
	if err != nil {
		t.Fatal(err)
	}
	if failed, err := plan.Capture(t.Context(), conn, ordinary, f, policy); err == nil || failed.Receipt() != nil {
		t.Fatal("ordinary DML gained virtual destination permission", err)
	}
	for _, d := range defs {
		s, err := r.Lookup(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		spec, _ := d.Plan.NativeInsert.Specification()
		max := strings.Repeat("a", spec.MaxBytes)
		if spec.MaxRunes != 0 {
			max = strings.Repeat("𠀀", spec.MaxRunes)
		}
		for _, text := range []string{"", "a", max} {
			if _, err := s.Bind(int64(1), text); err != nil {
				t.Fatal("legal slot payload refused", d.ID, err)
			}
		}
		for _, args := range [][]any{{int64(0), ""}, {int64(1), nil}, {int64(1), []byte{}}, {int64(1), "\xff"}, {int64(1), max + "a"}} {
			if _, err := s.Bind(args...); err == nil {
				t.Fatal("illegal native binding accepted", d.ID)
			}
		}
		if spec.MaxRunes != 0 {
			if _, err := s.Bind(int64(1), strings.Repeat("a", spec.MaxRunes+1)); err == nil {
				t.Fatal("normalized scalar envelope ignored")
			}
		}
	}
	encoded, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	var decoded plan.Golden
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, decoded.Receipt()); !errors.Is(err, stmt.ErrUnqualified) {
		t.Fatal("serialized native audit minted a receipt", err)
	}
	// Targeted native counterexamples: an identical-name TEMP destination,
	// an altered content relation, and altered persistent native options.
	for _, pair := range [][2]string{
		{"CREATE TEMP TABLE search_fragment_prefix(x)", "DROP TABLE temp.search_fragment_prefix"},
		{"DROP VIEW search_fragment_content", `CREATE VIEW search_fragment_content AS
SELECT * FROM search_fragments
WHERE search_generation_id=(SELECT id FROM search_generations WHERE bank_key='v1')`},
		{"INSERT INTO search_fragment_prefix_config(k,v) VALUES('automerge',8)", "DELETE FROM search_fragment_prefix_config WHERE k='automerge'"},
	} {
		if _, err := conn.ExecContext(t.Context(), pair[0]); err != nil {
			t.Fatal(err)
		}
		if failed, err := plan.Capture(t.Context(), conn, catalogue, f, policy); err == nil || failed.Receipt() != nil {
			t.Fatal("changed native format minted authority", err)
		}
		if _, err := conn.ExecContext(t.Context(), pair[1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestFTSInsertDescriptorDoesNotBroadenSQLOrKinds(t *testing.T) {
	for _, edit := range []func(*stmt.Definition){
		func(d *stmt.Definition) { d.Plan.NativeInsert = "other.vtab" },
		func(d *stmt.Definition) { d.SQL += " RETURNING rowid" },
		func(d *stmt.Definition) {
			d.SQL = "INSERT INTO search_fragment_prefix(search_fragment_prefix,prefix_tokens) VALUES(?,?)"
		},
		func(d *stmt.Definition) { d.Kind = stmt.Read },
		func(d *stmt.Definition) { d.Params[1].Nullable = true },
		func(d *stmt.Definition) { d.Plan.Hot = false },
	} {
		d := nativeFTSDefinitions()[0]
		edit(&d)
		if _, err := stmt.Describe([]stmt.Definition{d}); err == nil {
			t.Fatal("unsupported FTS operation/metadata accepted")
		}
	}
}
