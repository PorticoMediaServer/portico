package plan_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

var recordMGLCOctets = flag.Bool("record-mg-lc-octets", false, "deliberately record exact MG LC octet points")

func mgLCOctetDefinitions() []stmt.Definition {
	candidateSQL := "SELECT duration_ms,octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title) FROM lc_candidates INDEXED BY sqlite_autoindex_lc_candidates_1 WHERE generation_id=? AND rule_id=? AND item_id=?"
	entrySQL := "SELECT 113+octet_length(generation_id)+octet_length(occurrence_id)+octet_length(channel_id)+octet_length(asset_id)+octet_length(library_id)+octet_length(source_fence)+octet_length(title)+octet_length(rule_id)+octet_length(block_id)+octet_length(slate_reason) FROM lc_entries INDEXED BY sqlite_autoindex_lc_entries_1 WHERE generation_id=? AND occurrence_id=?"
	var out []stmt.Definition
	for _, shape := range []struct {
		id, sql, table string
		columns        []string
		params         []stmt.Param
		hit            []any
	}{
		{"writer.mg.lc.candidate-lengths", candidateSQL, "lc_candidates", []string{"generation_id", "rule_id", "item_id"}, []stmt.Param{{Name: "generation", Type: stmt.Text}, {Name: "rule", Type: stmt.Text}, {Name: "item", Type: stmt.Integer}}, []any{"generation", "rule", int64(1)}},
		{"writer.mg.lc.entry-lengths-point", entrySQL, "lc_entries", []string{"generation_id", "occurrence_id"}, []stmt.Param{{Name: "generation", Type: stmt.Text}, {Name: "occurrence", Type: stmt.Text}}, []any{"generation", "occurrence"}},
	} {
		index := "sqlite_autoindex_" + shape.table + "_1"
		pin := stmt.AutoIndexBinding{Table: shape.table, Name: index, Origin: "pk"}
		for _, name := range shape.columns {
			pin.Columns = append(pin.Columns, stmt.IndexColumn{Name: name, Collation: "BINARY"})
		}
		cohorts := []stmt.Cohort{{Name: "hit", Bindings: shape.hit}}
		for i := range shape.hit {
			miss := append([]any(nil), shape.hit...)
			if i == 2 {
				miss[i] = int64(2)
			} else {
				miss[i] = "missing"
			}
			cohorts = append(cohorts, stmt.Cohort{Name: shape.columns[i] + "_miss", Bindings: miss})
		}
		out = append(out, stmt.Definition{ID: stmt.ID(shape.id), SQL: shape.sql, Owner: "writer.mg.lc", CallSites: []string{"mg_lc_octets_test.go"}, Kind: stmt.Read, Params: shape.params, Cohorts: cohorts, Plan: stmt.Plan{Hot: true, Indexes: []string{index}, AutoIndexes: []stmt.AutoIndexBinding{pin}}})
	}
	return out
}

func mgLCOctetsFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	c, f, policy := issuerOctetsFixture(t)
	for _, sql := range []string{
		"INSERT INTO lc_channels(id,revision,config_json,enabled,position,name,state) VALUES('channel',1,'{}',1,0,'LC','ready')",
		"INSERT INTO lc_generations(id,channel_id,config_revision,config_json,catalog_fence,seed,status,phase,base_generation,start_ms,end_ms,boundary_ms,cursor_ms,created_ms) VALUES('generation','channel',1,'{}','fence','seed','pending','candidates','',0,1000,0,0,0)",
		"INSERT INTO lc_candidates(generation_id,rule_id,item_id,ordinal,library_id,asset_id,source_fence,duration_ms,title,show_id,season,episode,weight,sort_key) VALUES('generation','rule',1,0,'lib','asset','fence',1000,'Télé 📺',0,0,0,1.0,'sort')",
		"INSERT INTO lc_entries(generation_id,occurrence_id,channel_id,start_ms,end_ms,item_id,asset_id,library_id,source_fence,title,rule_id,block_id,source_offset_ms,slate_reason) VALUES('generation','occurrence','channel',0,1000,1,'asset','lib','fence','Télé 📺','rule','block',0,'')",
	} {
		if _, err := c.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	f.Implementations = "canonical MG LC full PK points; UTF8 title Télé television emoji; native byte counts; fixed entry recipe 113"

	return c, f, policy
}

func TestMGLCOctetPointsGolden(t *testing.T) {
	c, f, policy := mgLCOctetsFixture(t)
	defs := mgLCOctetDefinitions()
	r, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), c, r, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 7 {
		t.Fatal("incomplete guide octet cohorts")
	}
	registered, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	for i, d := range defs {
		st, err := registered.Lookup(d.ID)
		if err != nil {
			t.Fatal(err)
		}
		for j, cohort := range d.Cohorts {
			bound, err := st.Bind(cohort.Bindings...)
			if err != nil {
				t.Fatal(err)
			}
			var got, duration int64
			if i == 0 {
				err = bound.QueryRowContext(t.Context(), c).Scan(&duration, &got)
			} else {
				err = bound.QueryRowContext(t.Context(), c).Scan(&got)
			}
			if j > 0 {
				if err != sql.ErrNoRows {
					t.Fatal("miss", err)
				}
				continue
			}
			want := int64(len("assetlibfenceTélé 📺"))
			if i == 0 {
				if duration != 1000 {
					t.Fatal(duration)
				}
			} else {
				want += 113 + int64(len("generationoccurrencechannelruleblock"))
			}
			if err != nil || got != want {
				t.Fatal("actual UTF8 octets", got, want, err)
			}
		}
	}

	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "mg-lc-octets.json.gz")
	if *recordMGLCOctets {
		var buf bytes.Buffer
		z := gzip.NewWriter(&buf)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0644); err != nil {
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
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}

func TestMGLCOctetPointsClosed(t *testing.T) {
	c, f, policy := mgLCOctetsFixture(t)
	for _, original := range mgLCOctetDefinitions() {
		if len(plan.LintSQL(original.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), c, original.SQL)) == 0 {
			t.Fatal("generic octet grammar widened")
		}
		for _, name := range []string{"unknown_id", "range", "projection", "nullable", "key", "cold", "type", "seeks", "index", "origin", "collation", "descending", "constant", "cross_id"} {
			t.Run(string(original.ID)+"/"+name, func(t *testing.T) {
				data, err := json.Marshal(original)
				if err != nil {
					t.Fatal(err)
				}
				var d stmt.Definition
				if err := json.Unmarshal(data, &d); err != nil {
					t.Fatal(err)
				}
				// Restore native cohort types, not JSON floating-point bindings.
				d.Cohorts = original.Cohorts
				switch name {
				case "unknown_id":
					d.ID = "unregistered.octet.point"
				case "range":
					d.SQL = strings.Replace(d.SQL, "generation_id=?", "generation_id>?", 1)
				case "projection":
					d.SQL = strings.Replace(d.SQL, "octet_length(", "length(", 1)
				case "nullable":
					d.Params[0].Nullable = true
				case "key":
					d.Plan.AutoIndexes[0].Columns[0].Name = "other"
				case "type":
					d.Params[0].Type = stmt.Integer
				case "seeks":
					d.Plan.Seeks = []string{"generation_id=?"}
				case "index":
					d.Plan.Indexes = append(d.Plan.Indexes, "other")
				case "origin":
					d.Plan.AutoIndexes[0].Origin = "u"
				case "collation":
					d.Plan.AutoIndexes[0].Columns[0].Collation = "NOCASE"
				case "descending":
					d.Plan.AutoIndexes[0].Columns[0].Desc = true
				case "constant":
					d.SQL = strings.Replace(d.SQL, "113+", "114+", 1)
					if d.SQL == original.SQL {
						d.SQL = "SELECT 0," + strings.TrimPrefix(d.SQL, "SELECT ")
					}
				case "cross_id":
					if d.ID == "writer.mg.lc.candidate-lengths" {
						d.ID = "writer.mg.lc.entry-lengths-point"
					} else {
						d.ID = "writer.mg.lc.candidate-lengths"
					}

				case "cold":
					d.Plan.Hot = false
				}
				g, err := captureIssuerOctets(t, c, f, policy, d)
				if err == nil {
					t.Fatal("non-closed MG point accepted")
				}
				if _, err := stmt.New([]stmt.Definition{d}, g.Receipt()); err == nil {
					t.Fatal("failed capture minted receipt")
				}
			})
		}
	}
}

func TestMGLCOctetPointsRejectShadowedFunction(t *testing.T) {
	const childFlag = "PORTICO_MG_LC_OCTET_SHADOW_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMGLCOctetPointsRejectShadowedFunction$", "-test.count=1")
		cmd.Env = append(os.Environ(), childFlag+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native override child: %v\n%s", err, output)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	c, f, policy := mgLCOctetsFixture(t)
	for _, d := range mgLCOctetDefinitions() {
		if _, err := captureIssuerOctets(t, c, f, policy, d); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("shadowed guide preflight admitted", err)
		}
	}
}

// Each negative control starts from a fresh disposable canonical database. Only
// the target table is rebuilt; no domain draft or legacy database is imported.
func TestMGLCOctetPointsRejectNativeKeyDrift(t *testing.T) {
	for i, original := range mgLCOctetDefinitions() {
		table, key := "lc_candidates", "generation_id,rule_id,item_id"
		if i == 1 {
			table, key = "lc_entries", "generation_id,occurrence_id"
		}
		for _, change := range []string{"missing", "retargeted", "reordered", "nocase", "descending", "extra_key"} {
			t.Run(table+"/"+change, func(t *testing.T) {
				c, f, policy := mgLCOctetsFixture(t)
				var ddl string
				if err := c.QueryRowContext(t.Context(), "SELECT sql FROM sqlite_schema WHERE type='table' AND name=?", table).Scan(&ddl); err != nil {
					t.Fatal(err)
				}
				replacement := key
				switch change {
				case "missing":
					ddl = strings.Replace(ddl, "PRIMARY KEY("+key+"),", "", 1)
				case "retargeted":
					replacement = strings.Replace(key, "generation_id", "title", 1)
				case "reordered":
					parts := strings.Split(key, ",")
					parts[0], parts[1] = parts[1], parts[0]
					replacement = strings.Join(parts, ",")
				case "nocase":
					replacement = strings.Replace(key, "generation_id", "generation_id COLLATE NOCASE", 1)
				case "descending":
					replacement = strings.Replace(key, "generation_id", "generation_id DESC", 1)
				case "extra_key":
					replacement += ",title"
				}
				if change != "missing" {
					ddl = strings.Replace(ddl, "PRIMARY KEY("+key+")", "PRIMARY KEY("+replacement+")", 1)
				}
				if _, err := c.ExecContext(t.Context(), "DROP TABLE "+table); err != nil {
					t.Fatal(err)
				}
				if _, err := c.ExecContext(t.Context(), ddl); err != nil {
					t.Fatal(err)
				}
				g, err := captureIssuerOctets(t, c, f, policy, original)
				if err == nil {
					t.Fatal("native key drift admitted")
				}
				if _, err := stmt.New([]stmt.Definition{original}, g.Receipt()); err == nil {
					t.Fatal("failed capture minted receipt")
				}
			})
		}
	}
}
