package plan

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestMain(m *testing.M) { os.Exit(schematest.Run(m)) }

func iaAudioOctetsDefinitions() []stmt.Definition {
	var defs []stmt.Definition
	for _, v := range []struct {
		name, sql, table, seek string
		keys                   []string
	}{{"entity", iaAudioEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"entity", "library"}}, {"link", iaLinkOctetsSQL, "catalog_asset_links", "entity_id=? AND asset_id=?", []string{"entity", "asset", "library"}}} {
		d := stmt.Definition{ID: stmt.ID("read.foreground.ia.audio." + v.name + "_octets"), SQL: v.sql, Kind: stmt.Read, Owner: "store/read/ia_audio", CallSites: []string{"store/read/ia_audio.go:IAAudioMembership"}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{v.table}, Seeks: []string{v.seek}}}
		if v.name == "link" {
			d.ID = "read.foreground.ia.link_octets"
		}
		args := []any{int64(1), int64(1)}
		missing := []any{int64(999999), int64(1)}
		if v.name == "link" {
			args = []any{int64(1), int64(1), int64(1)}
			missing = []any{int64(999999), int64(1), int64(1)}
		}
		for _, k := range v.keys {
			d.Params = append(d.Params, stmt.Param{Name: k, Type: stmt.Integer})
		}
		d.Cohorts = []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: missing}}
		defs = append(defs, d)
	}
	return defs
}
func TestIAAudioOctetsNativeCapture(t *testing.T) {
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
	if _, e = source.Conn().ExecContext(t.Context(), `UPDATE catalog_asset_links SET edition=? WHERE entity_id=1 AND asset_id=1`, "é\x00x"); e != nil {
		t.Fatal(e)
	}
	defs := iaAudioOctetsDefinitions()
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
	f := Fixture{ConnectionPolicy: cfg, SHA256: readfixture.ForegroundHash(t), Statistics: Unanalyzed, Implementations: "IA audio entity/link octet points; canonical " + schema.SHA256 + "; title and edition e-acute,NUL,x"}
	g, e := Capture(context.Background(), c, cat, f, policy)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = stmt.New(defs, g.Receipt()); e != nil {
		t.Fatal(e)
	}
	var n int64
	if e = c.QueryRowContext(t.Context(), defs[1].SQL, int64(1), int64(1), int64(1)).Scan(&n, new(int64)); e != nil || n != 4 {
		t.Fatal("actual edition octets", n, e)
	}
	actual, e := g.JSON()
	if e != nil {
		t.Fatal(e)
	}
	path := "testdata/ia-audio-octets.json"
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
