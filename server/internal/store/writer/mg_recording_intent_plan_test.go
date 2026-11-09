package writer

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// The capture fixture is the existing canonical embedded schema plus one current
// source head, two channels/programmes and one enabled reservation window. It
// contains no authority, receipt, capture claim or performance population.
const mgRecordingCaptureFixtureSQL = `
INSERT INTO live_source_identities(id) VALUES('111111111111111111111111111111111111111111111111');
INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('222222222222222222222222222222222222222222222222','111111111111111111111111111111111111111111111111','Guide',1,2,2,'2026-11-07T00:00:00Z');
INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('111111111111111111111111111111111111111111111111','Live',7,'active',1,'222222222222222222222222222222222222222222222222','2026-11-07T00:00:00Z');
INSERT INTO live_channel_versions(generation_id,channel_id,provider_key,name,number,group_name,position,locator_sealed,available_start,available_end) VALUES('222222222222222222222222222222222222222222222222','4444444444444444444444444444444444444444444444444444444444444444','provider-1','Channel','1','Group',0,X'01','','');
INSERT INTO live_programmes(generation_id,id,channel_id,provider_key,title,start_utc,end_utc,lineage) VALUES('222222222222222222222222222222222222222222222222','5555555555555555555555555555555555555555555555555555555555555555','4444444444444444444444444444444444444444444444444444444444444444','p1','Programme','2026-12-01T00:00:00Z','2026-12-01T01:00:00Z','lineage');
INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES('6666666666666666666666666666666666666666666666666666666666666666','111111111111111111111111111111111111111111111111',1000,2000,0,1);
INSERT INTO dvr_owner_revisions(owner_key,revision) VALUES('7777777777777777777777777777777777777777777777777777777777777777',1);`

func mgRecordingCaptureExecutor(t *testing.T) *executor {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), mgRecordingCaptureFixtureSQL); err != nil {
		t.Fatal(err)
	}
	return w
}

// Native compile/Capture against the real embedded schema: no draft overlay and
// no stored golden is fabricated here. This proves every declared index/key,
// the complete foreign-key set and that Capture executes no effect.
func TestMGRecordingIntentStatementPlans(t *testing.T) {
	defs := mgRecordingIntentDefinitions()
	if len(defs) != 17 {
		t.Fatal("finite inventory", len(defs))
	}
	for _, d := range defs {
		if issues := plan.LintSQL(d.SQL); len(issues) != 0 {
			t.Fatal(d.ID, issues)
		}
	}
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	w := mgRecordingCaptureExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	digest := sha256.Sum256([]byte(mgRecordingCaptureFixtureSQL))
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Closed MG recording-intent point, current guide/source point, reservation window and owner-revision statements on the embedded schema; absent-key compile cohorts only; no authority, capture or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Manifest) != 17 || len(g.Plans) != 17 || g.ForeignKeys == nil {
		t.Fatal("incomplete native capture", len(g.Manifest), len(g.Plans), g.ForeignKeys == nil)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed accepting effects", before, after, err)
	}
}

// Every family member is SEARCH-only with no temporary B-tree, before and after
// ANALYZE. This is component evidence, not the Capture receipt above.
func TestMGRecordingIntentNativePlans(t *testing.T) {
	w := mgRecordingCaptureExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	for _, analyzed := range []bool{false, true} {
		if analyzed {
			if _, err := conn.ExecContext(t.Context(), `ANALYZE`); err != nil {
				t.Fatal(err)
			}
		}
		for _, d := range mgRecordingIntentDefinitions() {
			if issues := plan.LintSQLOn(t.Context(), conn, d.SQL); len(issues) > 0 {
				t.Fatal(d.ID, issues)
			}
			if d.Kind == stmt.Write && strings.HasPrefix(d.SQL, "INSERT ") {
				// A plain INSERT has no destination SEARCH; Capture audits its
				// destination indexes, keys and complete FK set instead.
				continue
			}
			rows, err := conn.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+d.SQL, d.Cohorts[0].Bindings...)
			if err != nil {
				t.Fatal(d.ID, err)
			}
			count := 0
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(detail, "SEARCH ") || strings.Contains(detail, "TEMP B-TREE") {
					t.Fatal(d.ID, detail)
				}
				count++
			}
			err = rows.Err()
			rows.Close()
			if err != nil || count == 0 {
				t.Fatal(d.ID, count, err)
			}
		}
	}
}

// The family's own descriptors keep the exact IDs and NORMAL durability without
// widening any shared registry list.
func TestMGRecordingIntentDescriptors(t *testing.T) {
	got := mgRecordingIntentDescriptors()
	want := []Descriptor{
		{MGScheduleRecording, "mg.recording.schedule", Normal},
		{MGUpdateRecording, "mg.recording.update", Normal},
		{MGCancelRecording, "mg.recording.cancel", Normal},
		{MGKeepRecording, "mg.recording.keep", Normal},
	}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(i, got[i], want[i])
		}
	}
	for _, d := range want {
		if _, err := Describe(d.ID); err != nil {
			t.Fatal(err)
		}
	}
	// The closed purpose guard admits only this family and only NORMAL.
	if err := mgRecordingPurpose(nil, MGScheduleRecording); err == nil {
		t.Fatal("nil turn admitted a purpose")
	}
}
