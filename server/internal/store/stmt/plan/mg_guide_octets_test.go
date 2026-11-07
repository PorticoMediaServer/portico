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

var recordMGGuideOctets = flag.Bool("record-mg-guide-octets", false, "deliberately record exact MG guide octet points")

func mgGuideOctetDefinitions() []stmt.Definition {
	channelSQL := "SELECT octet_length(channel_id),octet_length(name),octet_length(number),octet_length(group_name),octet_length(logo_url) FROM live_channel_versions INDEXED BY sqlite_autoindex_live_channel_versions_1 WHERE generation_id=? AND channel_id=?"
	programmeSQL := "SELECT octet_length(id),octet_length(channel_id),octet_length(title),octet_length(start_utc),octet_length(end_utc),octet_length(metadata_series_id),octet_length(metadata_episode_id),octet_length(metadata_new_evidence),octet_length(metadata_description),octet_length(metadata_facts),octet_length(icon_url) FROM live_programmes INDEXED BY sqlite_autoindex_live_programmes_1 WHERE generation_id=? AND id=?"
	var out []stmt.Definition
	for i, shape := range []struct{ name, sql, table, identity string }{
		{"channel_octets", channelSQL, "live_channel_versions", "channel_id"},
		{"programme_octets", programmeSQL, "live_programmes", "id"},
	} {
		id := "channel"
		if i == 1 {
			id = "programme"
		}
		index := "sqlite_autoindex_" + shape.table + "_1"
		out = append(out, stmt.Definition{ID: stmt.ID("read.mg.guide." + shape.name), SQL: shape.sql, Owner: "MG guide byte preflight", CallSites: []string{"mg_guide_octets_test.go"}, Kind: stmt.Read,
			Params:  []stmt.Param{{Name: "generation", Type: stmt.Text}, {Name: "identity", Type: stmt.Text}},
			Cohorts: []stmt.Cohort{{Name: "hit", Bindings: []any{"generation", id}}, {Name: "missing_generation", Bindings: []any{"missing", id}}, {Name: "missing_identity", Bindings: []any{"generation", "missing"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{"generation_id=? AND " + shape.identity + "=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: shape.table, Name: index, Origin: "pk", Columns: []stmt.IndexColumn{{Name: "generation_id", Collation: "BINARY"}, {Name: shape.identity, Collation: "BINARY"}}}}},
		})
	}
	return out
}

func mgGuideOctetsFixture(t *testing.T) (*sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	c, f, policy := issuerOctetsFixture(t)
	for _, sql := range []string{
		"INSERT INTO live_source_identities(id) VALUES('source')",
		"INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('generation','source','Guide',1,1,1,'2026-11-07T00:00:00Z')",
		"INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('generation','channel','provider','Télé 📺','1','News',0,X'01','','')",
		"INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('generation','programme','channel','provider','Émission 漢字','2026-11-07T00:00:00Z','2026-11-07T01:00:00Z','lineage')",
	} {
		if _, err := c.ExecContext(t.Context(), sql); err != nil {
			t.Fatal(err)
		}
	}
	f.Implementations = "canonical MG guide point; channel Télé television emoji; programme Émission Han; nullable fields retained; native octet counts"
	return c, f, policy
}

func TestMGGuideOctetPointsGolden(t *testing.T) {
	c, f, policy := mgGuideOctetsFixture(t)
	defs := mgGuideOctetDefinitions()
	r, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	g, err := plan.Capture(t.Context(), c, r, f, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 6 {
		t.Fatal("incomplete guide octet cohorts")
	}
	registered, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, id string
		want     []any
	}{
		{"channel_octets", "channel", []any{int64(7), int64(len("Télé 📺")), int64(1), int64(4), nil}},
		{"programme_octets", "programme", []any{int64(9), int64(7), int64(len("Émission 漢字")), int64(20), int64(20), nil, nil, nil, nil, int64(2), nil}},
	} {
		s, err := registered.Lookup(stmt.ID("read.mg.guide." + tc.name))
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Bind("generation", tc.id)
		if err != nil {
			t.Fatal(err)
		}
		got := make([]any, len(tc.want))
		ptrs := make([]any, len(got))
		for i := range ptrs {
			ptrs[i] = &got[i]
		}
		if err := b.QueryRowContext(t.Context(), c).Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Fatal("actual point octets", tc.name, i, got, tc.want)
			}
		}
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("testdata", "mg-guide-octets.json.gz")
	if *recordMGGuideOctets {
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

func TestMGGuideOctetPointsClosed(t *testing.T) {
	c, f, policy := mgGuideOctetsFixture(t)
	for _, original := range mgGuideOctetDefinitions() {
		if len(plan.LintSQL(original.SQL)) == 0 || len(plan.LintSQLOn(t.Context(), c, original.SQL)) == 0 {
			t.Fatal("generic octet grammar widened")
		}
		for _, name := range []string{"unknown_id", "range", "projection", "nullable", "key", "cold"} {
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

func TestMGGuideOctetPointsRejectShadowedFunction(t *testing.T) {
	const childFlag = "PORTICO_MG_OCTET_SHADOW_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMGGuideOctetPointsRejectShadowedFunction$", "-test.count=1")
		cmd.Env = append(os.Environ(), childFlag+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native override child: %v\n%s", err, output)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) { return int64(777), nil }); err != nil {
		t.Fatal(err)
	}
	c, f, policy := mgGuideOctetsFixture(t)
	for _, d := range mgGuideOctetDefinitions() {
		if _, err := captureIssuerOctets(t, c, f, policy, d); err == nil || !strings.Contains(err.Error(), "unshadowed native") {
			t.Fatal("shadowed guide preflight admitted", err)
		}
	}
}
