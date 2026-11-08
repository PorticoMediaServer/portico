package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func iaTVOctetsDefinitions() []stmt.Definition {
	entity := func(name string) stmt.Definition {
		return stmt.Definition{
			ID: stmt.ID("read.foreground.ia.tv." + name + "_octets"), SQL: iaTVEntityOctetsSQL, Kind: stmt.Read,
			Owner: "store/read/ia_tv", CallSites: []string{"store/read/ia_tv.go"},
			Params:  []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}},
		}
	}
	link := stmt.Definition{
		ID: "read.foreground.ia.tv.link_octets", SQL: iaTVLinkOctetsSQL, Kind: stmt.Read,
		Owner: "store/read/ia_tv", CallSites: []string{"store/read/ia_tv.go"},
		Params:  []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
	}
	asset := stmt.Definition{
		ID: "read.foreground.ia.tv.asset_octets", SQL: iaTVAssetOctetsSQL, Kind: stmt.Read,
		Owner: "store/read/ia_tv", CallSites: []string{"store/read/ia_tv.go"},
		Params:  []stmt.Param{{Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1)}}},
		Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
	}
	return []stmt.Definition{entity("show"), entity("season"), entity("episode"), link, asset}
}

func TestIATVOctetsNativeCapture(t *testing.T) {
	source := readfixture.Foreground(t)
	cfg := readfixture.ReaderConfig()
	db, _, e := sqliteconn.Open(t.Context(), source.Path(), cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	c, e := db.Conn(t.Context())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	// NUL and multibyte bytes falsify character-count accounting.
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE catalog_entities SET title=? WHERE id=1`, "é\x00x"); e != nil {
		t.Fatal(e)
	}
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE catalog_asset_links SET edition='édition 🦊' WHERE entity_id=1 AND asset_id=1`); e != nil {
		t.Fatal(e)
	}
	defs := iaTVOctetsDefinitions()
	cat, e := stmt.Describe(defs)
	if e != nil {
		t.Fatal(e)
	}
	data, e := schema.ReadContract("INT-INDEX-POLICY.json")
	if e != nil {
		t.Fatal(e)
	}
	policy, e := ReadIndexPolicy(data)
	if e != nil {
		t.Fatal(e)
	}
	f := Fixture{ConnectionPolicy: cfg, SHA256: readfixture.ForegroundHash(t), Statistics: Unanalyzed, Implementations: "IA TV entity/link/asset octet points; canonical " + schema.SHA256 + "; multibyte,NUL,emoji"}
	g, e := Capture(context.Background(), c, cat, f, policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = stmt.New(defs, g.Receipt()); e != nil {
		t.Fatal(e)
	}
	var title int64
	if e = c.QueryRowContext(t.Context(), defs[2].SQL, int64(1), int64(1)).Scan(new([]byte), &title, new([]byte)); e != nil || title != 4 {
		t.Fatal("actual title octets", title, e)
	}
	actual, e := g.JSON()
	if e != nil {
		t.Fatal(e)
	}
	path := "testdata/ia-tv-octets.json"
	if dir := os.Getenv("PORTICO_LANE_EVIDENCE_DIR"); dir != "" {
		if e = os.MkdirAll(dir, 0755); e != nil {
			t.Fatal(e)
		}
		if e = os.WriteFile(filepath.Join(dir, filepath.Base(path)), actual, 0644); e != nil {
			t.Fatal(e)
		}
	}
	expected, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	if e = g.Compare(expected); e != nil {
		t.Fatal(e)
	}
}
