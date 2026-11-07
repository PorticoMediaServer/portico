package plan_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

func TestPlanCanonicalSchemaFKAndStatementContracts(t *testing.T) {
	conn, _, f, _ := fixture(t)
	ctx := context.Background()
	if _, err := conn.ExecContext(ctx, "DROP TABLE items"); err != nil {
		t.Fatal(err)
	}
	if err := schema.InitializeEmpty(ctx, conn); err != nil {
		t.Fatal(err)
	}
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	fk, err := plan.ForeignKeys(ctx, conn, f.ConnectionPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CheckSchemaForeignKeys(&fk); err != nil {
		t.Fatal(err)
	}
	checkFKGolden(t, fk)
	registry := canonicalRegistry(t, policy)
	golden, err := plan.Capture(ctx, conn, registry, plan.Fixture{ConnectionPolicy: f.ConnectionPolicy, SHA256: schema.SHA256, Statistics: plan.Unanalyzed, Implementations: "bundled SQLite 3.53.4; builtin functions/collations; empty canonical schema"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	// All 28 cohorts were independently regenerated on approved 291ab3ac,
	// 567267b7, 5f3c7f67, abf43774 and adopted 45c96755 with the same VM canonicalization. EQP
	// notused is now also a placeholder; removing forced autoindex names
	// changes no other field. Stop before
	// recording if any opcode/operand/EQP beyond physical identifiers changes.
	const retainedPlansSHA256 = "6473c4cff5b8aa01db48d7eb91663f19c72f43523aaf705fa24dec419641800c"
	plansJSON, err := json.Marshal(golden.Plans)
	if err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(plansJSON)); got != retainedPlansSHA256 {
		t.Fatalf("STOP: retained 28-cohort VM/EQP changed: expected %s, actual %s", retainedPlansSHA256, got)
	}
	path := filepath.Join("testdata", "canonical.json")
	if *record {
		bytes, err := golden.JSON()
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, append(bytes, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := golden.Compare(bytes); err != nil {
		t.Fatal(err)
	}
	checkCanonicalSourceFacts(t, conn, registered(t, registry, golden))
	t.Logf("schema %s: %d FKs, %d actual DELETE/UPDATE/parent-collated probes; %d statement/cohort plans", schema.SHA256, len(fk.Plans), 3*len(fk.Plans), len(golden.Plans))
}

// These are contract probes of the new schema, not substitute W/read adapters
// or evidence that every future product statement has been registered.
func canonicalRegistry(t *testing.T, policy plan.IndexPolicy) *stmt.Registry {
	t.Helper()
	defs := []stmt.Definition{}
	add := func(id, sql string, params []stmt.Param, cohorts []stmt.Cohort, contract stmt.Plan) {
		defs = append(defs, stmt.Definition{ID: stmt.ID(id), SQL: sql, Owner: "schema-contract-harness", CallSites: []string{"canonical_test.go"}, Kind: stmt.Read, Params: params, Cohorts: cohorts, Plan: contract})
	}
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	partial := func(index string) string {
		p, err := policy.Predicate(index)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	add("identity.by_source", "SELECT subject_id FROM catalog_identities WHERE library_id=? AND source_key=?", []stmt.Param{integer("library"), text("source")}, []stmt.Cohort{{Name: "overlapping-library-1", Bindings: []any{int64(1), "same/path"}}, {Name: "overlapping-library-2", Bindings: []any{int64(2), "same/path"}}}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_identities"}, Seeks: []string{"library_id=? AND source_key=?"}})
	add("identity.by_subject", "SELECT source_key FROM catalog_identities INDEXED BY catalog_identities_subject WHERE subject_id=? AND library_id=? ORDER BY source_key", []stmt.Param{integer("subject"), integer("library")}, []stmt.Cohort{{Name: "retained", Bindings: []any{int64(1), int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_identities_subject"}, Seeks: []string{"subject_id=? AND library_id=?"}})
	add("asset.by_path", "SELECT id,token FROM catalog_assets WHERE library_id=? AND path=?", []stmt.Param{integer("library"), text("path")}, []stmt.Cohort{{Name: "overlapping-library-1", Bindings: []any{int64(1), "same/path"}}, {Name: "overlapping-library-2", Bindings: []any{int64(2), "same/path"}}}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_3"}, Seeks: []string{"library_id=? AND path=?"}})
	add("contribution.top_size", "SELECT file_size,asset_id FROM catalog_asset_contributions INDEXED BY catalog_asset_contribution_size WHERE parent_id=? ORDER BY file_size DESC,asset_id LIMIT 1", []stmt.Param{integer("parent")}, []stmt.Cohort{{Name: "empty", Bindings: []any{int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_contribution_size"}, Seeks: []string{"parent_id=?"}})
	add("contribution.top_duration", "SELECT duration,contributor_id,source FROM catalog_scalar_contributions INDEXED BY catalog_scalar_duration WHERE parent_id=? AND "+partial("catalog_scalar_duration")+" ORDER BY duration DESC,contributor_id,source LIMIT 1", []stmt.Param{integer("parent")}, []stmt.Cohort{{Name: "nonnull-extrema", Bindings: []any{int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_scalar_duration"}, Seeks: []string{"parent_id=?"}})
	add("authored.asset_union", "SELECT parent_id FROM catalog_asset_contributions INDEXED BY catalog_asset_authored WHERE asset_id=? AND "+partial("catalog_asset_authored")+" ORDER BY parent_id LIMIT ?", []stmt.Param{integer("asset"), integer("limit")}, []stmt.Cohort{{Name: "cap-plus-one", Bindings: []any{int64(1), int64(101)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_authored"}, Seeks: []string{"asset_id=?"}})
	for _, direction := range []string{"asc", "desc"} {
		order, compare := "ASC", ">"
		after := int64(0)
		if direction == "desc" {
			order, compare = "DESC", "<"
			after = 1 << 62
		}
		base := "SELECT id,resolution_key FROM catalog_entities INDEXED BY catalog_entities_resolution WHERE library_id=? AND kind=? AND " + partial("catalog_entities_resolution")
		ordered := " ORDER BY resolution_key " + order + ",id " + order + " LIMIT ?"
		prefix := []stmt.Param{integer("library"), integer("kind")}
		chronologyBase := "SELECT id,added_at FROM catalog_entities INDEXED BY catalog_entities_added_v1 WHERE library_id=? AND kind=?"
		chronologyOrder := " ORDER BY added_at " + order + ",id " + order + " LIMIT ?"
		add("chronology."+direction+".first", chronologyBase+" AND added_at IS NOT NULL"+chronologyOrder, []stmt.Param{integer("library"), integer("kind"), integer("limit")}, []stmt.Cohort{{Name: "first", Bindings: []any{int64(1), int64(1), int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_added_v1"}})
		add("chronology."+direction+".tail", chronologyBase+" AND added_at=? AND id"+compare+"?"+chronologyOrder, []stmt.Param{integer("library"), integer("kind"), text("key"), integer("after"), integer("limit")}, []stmt.Cohort{{Name: "equal-instant", Bindings: []any{int64(1), int64(1), "2026-11-04T12:00:00.000000000Z", int64(7), int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_added_v1"}, Seeks: []string{"added_at=? AND id" + compare + "?"}})
		add("chronology."+direction+".next", chronologyBase+" AND added_at"+compare+"?"+chronologyOrder, []stmt.Param{integer("library"), integer("kind"), text("key"), integer("limit")}, []stmt.Cohort{{Name: "next-instant", Bindings: []any{int64(1), int64(1), "2026-11-04T12:00:00.000000000Z", int64(1)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_added_v1"}, Seeks: []string{"added_at" + compare + "?"}})
		titleBase := "SELECT id,sort_key FROM catalog_entities INDEXED BY catalog_entities_title WHERE library_id=? AND kind=?"
		titleOrder := " ORDER BY sort_key COLLATE NOCASE " + order + ",id " + order + " LIMIT ?"
		add("title."+direction+".first", titleBase+titleOrder, []stmt.Param{integer("library"), integer("kind"), integer("limit")}, []stmt.Cohort{{Name: "first", Bindings: []any{int64(1), int64(1), int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_title"}, Seeks: []string{"library_id=? AND kind=?"}})
		add("title."+direction+".tail", titleBase+" AND sort_key COLLATE NOCASE=? AND id"+compare+"?"+titleOrder, []stmt.Param{integer("library"), integer("kind"), text("key"), integer("after"), integer("limit")}, []stmt.Cohort{{Name: "case-insensitive-tail", Bindings: []any{int64(1), int64(1), "A", int64(500), int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_title"}, Seeks: []string{"sort_key=? AND id" + compare + "?"}})
		add("title."+direction+".next", titleBase+" AND sort_key COLLATE NOCASE"+compare+"?"+titleOrder, []stmt.Param{integer("library"), integer("kind"), text("key"), integer("limit")}, []stmt.Cohort{{Name: "next-key", Bindings: []any{int64(1), int64(1), "a", int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_title"}, Seeks: []string{"sort_key" + compare + "?"}})
		add("resolution."+direction+".first", base+" AND resolution_key IS NOT NULL"+ordered, append(append([]stmt.Param{}, prefix...), integer("limit")), []stmt.Cohort{{Name: "first", Bindings: []any{int64(1), int64(1), int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_resolution"}})
		add("resolution."+direction+".tail", base+" AND resolution_key=? AND id"+compare+"?"+ordered, append(append([]stmt.Param{}, prefix...), integer("key"), integer("after"), integer("limit")), []stmt.Cohort{{Name: "equal-tail", Bindings: []any{int64(1), int64(1), int64(1080), int64(500), int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_resolution"}, Seeks: []string{"resolution_key=? AND id" + compare + "?"}})
		add("resolution."+direction+".next", base+" AND resolution_key"+compare+"?"+ordered, append(append([]stmt.Param{}, prefix...), integer("key"), integer("limit")), []stmt.Cohort{{Name: "next-key", Bindings: []any{int64(1), int64(1), int64(1080), int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_resolution"}, Seeks: []string{"resolution_key" + compare + "?"}})
		add("resolution."+direction+".null", base+" AND resolution_key IS NULL AND id"+compare+"?"+ordered, append(append([]stmt.Param{}, prefix...), integer("after"), integer("limit")), []stmt.Cohort{{Name: "null-stream", Bindings: []any{int64(1), int64(1), after, int64(7)}}}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_resolution"}, Seeks: []string{"resolution_key=? AND id" + compare + "?"}})
	}
	registry, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func checkFKGolden(t *testing.T, plans plan.ForeignKeyAudit) {
	t.Helper()
	actual, err := json.MarshalIndent(plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "canonical-fk.json.gz")
	if *record {
		var compressed bytes.Buffer
		gz := gzip.NewWriter(&compressed)
		if _, err := gz.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, compressed.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	expected, err := io.ReadAll(gz)
	closeErr := gz.Close()
	if err != nil {
		t.Fatal(err)
	}
	if closeErr != nil {
		t.Fatal(closeErr)
	}
	if err := plan.CompareExactJSON(expected, actual); err != nil {
		t.Fatalf("canonical FK audit: %v", err)
	}
}
