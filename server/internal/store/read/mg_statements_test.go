package read

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/schematest"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGGuidePlans = flag.Bool("record-mg-guide-plans", false, "deliberately record MG guide source plans")

// Independent facts: time ties, overlap boundaries, two immutable generations,
// wide multibyte display values, and a sealed locator never selected by reads.
const mgGuideSeed = `
INSERT INTO live_source_identities(id,settings_viewer_access) VALUES('mg-source','server-members');
INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES
 ('mg-old','mg-source','Old guide',2,3,6,'2026-11-07T00:00:00Z'),
 ('mg-new','mg-source','New guide',2,1,1,'2026-11-07T00:00:00Z');
INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('mg-source','Guide',1,'active',2,'mg-old','2026-11-07T00:00:00Z');
INSERT INTO live_operations(request_id,input_hash,source_id,generation_id,expected_revision,status) VALUES('mg-import','digest','mg-source','mg-new',1,'staging');
INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end,has_live_channel_logos,logo_url) VALUES
 ('mg-old','mg-channel','provider','Télé 📺','1','News',0,X'010203','2026-11-07T00:00:00Z','2026-11-07T06:00:00Z',1,'https://example.invalid/logo'),
 ('mg-old','mg-channel-1','provider-1','Second','2','News',1,X'010203','','',0,NULL),
 ('mg-old','mg-channel-2','provider-2','Third','3','News',2,X'010203','','',0,NULL),
 ('mg-new','mg-channel','provider','New channel','1','News',0,X'010203','','',0,NULL);
INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage,has_live_programme_metadata,metadata_series_id,metadata_episode_id,metadata_new_evidence,metadata_description,metadata_facts,has_live_programme_icons,icon_url) VALUES
 ('mg-old','mg-a','mg-channel','provider-a','Émission 📺','2026-11-07T00:00:00Z','2026-11-07T01:00:00Z','a',1,'series','episode','new','Résumé 漢字','{"rating":"TV-G"}',1,'https://example.invalid/icon'),
 ('mg-new','mg-a','mg-channel','provider-a','Replacement','2026-11-07T00:00:00Z','2026-11-07T01:00:00Z','a',0,NULL,NULL,NULL,NULL,'{}',0,NULL);
INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES
 ('mg-old','mg-b','mg-channel','provider-b','Tie one','2026-11-07T01:00:00Z','2026-11-07T02:00:00Z','b'),
 ('mg-old','mg-c','mg-channel','provider-c','Tie two','2026-11-07T01:00:00Z','2026-11-07T02:00:00Z','c'),
 ('mg-old','mg-before','mg-channel','provider-before','Ends at start','2026-11-06T23:00:00Z','2026-11-07T00:00:00Z','before'),
 ('mg-old','mg-overlap','mg-channel','provider-overlap','Starts before','2026-11-06T23:30:00Z','2026-11-07T00:30:00Z','overlap'),
 ('mg-old','mg-after','mg-channel','provider-after','Starts at end','2026-11-07T06:00:00Z','2026-11-07T07:00:00Z','after');
`

type mgGuideFixture struct {
	path   string
	writer *sql.Conn
}

func newMGGuideFixture(t *testing.T) *mgGuideFixture {
	t.Helper()
	f := &mgGuideFixture{path: schematest.Clone(t)}
	cfg := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	f.writer, err = db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.writer.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err = f.writer.ExecContext(t.Context(), mgGuideSeed); err != nil {
		t.Fatal(err)
	}
	return f
}

func mgGuideReader(t *testing.T, f *mgGuideFixture) *sql.Conn {
	t.Helper()
	db, _, err := sqliteconn.Open(t.Context(), f.path, browseReaderConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	c, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(); err != nil {
			t.Error(err)
		}
	})
	return c
}

func captureMGGuide(t *testing.T, c *sql.Conn, defs []stmt.Definition, statistics plan.Statistics) plan.Golden {
	t.Helper()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
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
	hash := sha256.Sum256([]byte(mgGuideSeed))
	g, err := plan.Capture(t.Context(), c, catalogue, plan.Fixture{
		ConnectionPolicy: browseReaderConfig(), SHA256: hex.EncodeToString(hash[:]), Statistics: statistics,
		Implementations: "canonical " + schema.SHA256 + "; bundled SQLite3.53.4; mgGuideSeed source facts; inert source primitives, no authority or latency qualification",
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func mgGuideRegistry(t *testing.T, c *sql.Conn) *stmt.Registry {
	t.Helper()
	defs := mgGuideDefinitions()
	g := captureMGGuide(t, c, defs, plan.Unanalyzed)
	r, err := stmt.New(defs, g.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func mgGuideQuery(t *testing.T, ctx context.Context, c *sql.Conn, r *stmt.Registry, name string, args ...any) [][]any {
	t.Helper()
	s, err := r.Lookup(mgGuideID(name))
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.Bind(args...)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := b.QueryContext(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range ptrs {
			ptrs[i] = &values[i]
		}
		if err = rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		out = append(out, values)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestMGGuideSourcePlans(t *testing.T) {
	f := newMGGuideFixture(t)
	c := mgGuideReader(t, f)
	var captures []plan.Golden
	for _, stats := range []plan.Statistics{plan.Unanalyzed, plan.Analyzed} {
		if stats == plan.Analyzed {
			if _, err := f.writer.ExecContext(t.Context(), "ANALYZE"); err != nil {
				t.Fatal(err)
			}
		}
		g := captureMGGuide(t, c, mgGuideDefinitions(), stats)
		if len(g.Manifest) != 7 || len(g.Plans) != 19 {
			t.Fatal("changed closed inventory", len(g.Manifest), len(g.Plans))
		}
		if _, err := stmt.New(mgGuideDefinitions(), g.Receipt()); err != nil {
			t.Fatal(err)
		}
		captures = append(captures, g)
	}
	actual, err := json.MarshalIndent(captures, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-guide-plans.json.gz"
	if *recordMGGuidePlans {
		var data bytes.Buffer
		z := gzip.NewWriter(&data)
		if _, err := z.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := z.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data.Bytes(), 0644); err != nil {
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
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}

func TestMGGuideNativeWindowAndOctets(t *testing.T) {
	f := newMGGuideFixture(t)
	c := mgGuideReader(t, f)
	r := mgGuideRegistry(t, c)
	if _, err := c.ExecContext(t.Context(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	var positions []int64
	after := int64(-1)
	for n := 0; n < 3; n++ {
		page := mgGuideQuery(t, t.Context(), c, r, "channels", "mg-old", after, int64(2))
		for _, row := range page {
			positions = append(positions, row[0].(int64))
			after = row[0].(int64)
		}
		if len(page) < 2 {
			break
		}
	}
	if !reflect.DeepEqual(positions, []int64{0, 1, 2}) {
		t.Fatal("channel keyset lost or duplicated a position", positions)
	}
	channel := mgGuideQuery(t, t.Context(), c, r, "channel_octets", "mg-old", "mg-channel")[0]
	value := mgGuideQuery(t, t.Context(), c, r, "channel", "mg-old", "mg-channel")[0]
	for i, col := range []int{0, 1, 2, 3, 6} {
		if channel[i] != int64(len(value[col].(string))) {
			t.Fatal("channel preflight not actual octets", i, channel, value)
		}
	}
	windowArgs := []any{"mg-old", "mg-channel", "2026-11-07T06:00:00Z", "2026-11-07T00:00:00Z", int64(5)}
	rows := mgGuideQuery(t, t.Context(), c, r, "programmes", windowArgs...)
	var ids []string
	for _, row := range rows {
		ids = append(ids, row[0].(string))
	}
	slices.Sort(ids)
	if !reflect.DeepEqual(ids, []string{"mg-a", "mg-b", "mg-c", "mg-overlap"}) {
		t.Fatal("interval overlap/tie membership", ids)
	}
	preflight := mgGuideQuery(t, t.Context(), c, r, "programme_octets", "mg-old", "mg-a")[0]
	p := mgGuideQuery(t, t.Context(), c, r, "programme", "mg-old", "mg-a")[0]
	for i, col := range []int{0, 1, 2, 3, 4, 6, 7, 8, 9, 10, 12} {
		if preflight[i] != int64(len(p[col].(string))) {
			t.Fatal("programme preflight not actual octets", i, preflight, p)
		}
	}
	windowArgs[4] = int64(3)
	if got := mgGuideQuery(t, t.Context(), c, r, "programmes", windowArgs...); len(got) != 3 {
		t.Fatal("overflow sentinel must fill max+1 for caller capacity decision", got)
	}
	if got := mgGuideQuery(t, t.Context(), c, r, "programme", "mg-new", "mg-a"); got[0][2] != "Replacement" {
		t.Fatal("generation-scoped identity", got)
	}
}

func TestMGGuideStagingWriterDoesNotGatePublishedRead(t *testing.T) {
	f := newMGGuideFixture(t)
	c := mgGuideReader(t, f)
	r := mgGuideRegistry(t, c)
	if _, err := f.writer.ExecContext(t.Context(), "BEGIN IMMEDIATE"); err != nil {
		t.Fatal(err)
	}
	defer f.writer.ExecContext(context.Background(), "ROLLBACK")
	if _, err := f.writer.ExecContext(t.Context(), "UPDATE live_operations SET input_hash='staged' WHERE request_id='mg-import'"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if got := mgGuideQuery(t, ctx, c, r, "source", "mg-source"); got[0][2] != "mg-old" {
		t.Fatal("staging changed visible head", got)
	}
	if _, err := c.ExecContext(t.Context(), "BEGIN"); err != nil {
		t.Fatal(err)
	}
	defer c.ExecContext(context.Background(), "ROLLBACK")
	mgGuideQuery(t, t.Context(), c, r, "source", "mg-source") // establish the old snapshot
	if _, err := f.writer.ExecContext(t.Context(), "UPDATE live_sources SET active_generation='mg-new' WHERE id='mg-source'; UPDATE live_operations SET status='published' WHERE request_id='mg-import'; COMMIT"); err != nil {
		t.Fatal(err)
	}
	if got := mgGuideQuery(t, t.Context(), c, r, "source", "mg-source"); got[0][2] != "mg-old" {
		t.Fatal("snapshot mixed published generations", got)
	}
	if got := mgGuideQuery(t, t.Context(), c, r, "programme", "mg-old", "mg-a"); got[0][2] != "Émission 📺" {
		t.Fatal("old immutable value lost", got)
	}
	if _, err := c.ExecContext(t.Context(), "ROLLBACK"); err != nil {
		t.Fatal(err)
	}
	if got := mgGuideQuery(t, t.Context(), c, r, "source", "mg-source"); got[0][2] != "mg-new" {
		t.Fatal("new snapshot missed publication", got)
	}
	if got := mgGuideQuery(t, t.Context(), c, r, "programme", "mg-new", "mg-a"); got[0][2] != "Replacement" {
		t.Fatal("replacement not visible", got)
	}
}

func TestMGGuideCaptureRejectsUnsafePlans(t *testing.T) {
	f := newMGGuideFixture(t)
	c := mgGuideReader(t, f)
	defs := mgGuideDefinitions()
	original := defs[1]
	positive := captureMGGuide(t, c, []stmt.Definition{original}, plan.Unanalyzed)
	if _, err := stmt.New([]stmt.Definition{original}, positive.Receipt()); err != nil {
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
	for name, sql := range map[string]string{
		"unforced":             strings.Replace(original.SQL, " INDEXED BY sqlite_autoindex_live_channel_versions_2", "", 1),
		"temporary_sort":       strings.Replace(original.SQL, "ORDER BY position", "ORDER BY name", 1),
		"unbounded_generation": strings.Replace(original.SQL, "generation_id=? AND position>?", "position>? AND ? IS NOT NULL", 1),
	} {
		t.Run(name, func(t *testing.T) {
			d := original
			d.SQL = sql
			catalogue, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				t.Fatal(err)
			}
			hash := sha256.Sum256([]byte(mgGuideSeed))
			_, err = plan.Capture(t.Context(), c, catalogue, plan.Fixture{ConnectionPolicy: browseReaderConfig(), SHA256: hex.EncodeToString(hash[:]), Statistics: plan.Unanalyzed, Implementations: "MG negative control"}, policy)
			if err == nil {
				t.Fatal("unsafe guide plan accepted")
			}
			t.Logf("intended rejection: %v", err)
		})
	}
	// Receipts authorize only exact captured definitions, never edited SQL.
	changed := original
	changed.SQL = strings.Replace(original.SQL, "ORDER BY position", "ORDER BY position DESC", 1)
	if _, err := stmt.New([]stmt.Definition{changed}, positive.Receipt()); err == nil {
		t.Fatal("partial/mismatched Capture receipt accepted")
	}
}
