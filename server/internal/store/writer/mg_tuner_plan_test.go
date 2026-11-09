package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordMGTunerPlans = flag.Bool("record-mg-tuner-plans", false, "record closed MG tuner point/bounded-stream plans")

// The capture fixture is the existing canonical empty schema plus the immutable
// source head, its current channel, one held allocation and one enabled
// reservation window. Nothing here is authority, a receipt or a performance
// population.
const mgTunerCaptureFixtureSQL = mgSourceFixtureSQL + mgTunerFixtureSQL + `
INSERT INTO live_allocations(id,source_id,channel_id,resource_id,kind,authority,account_id,profile_id,token,generation,state,lease_until_ms,created_ms,preempt_at_ms) VALUES('held','source','channel','held-resource','live','local','issuer','issuer-profile','held-token',1,'active',7200000,0,0);
INSERT INTO live_reservations(id,source_id,start_ms,end_ms,priority,enabled) VALUES('reserved','source',7190000,7250000,0,1);`

func mgTunerCaptureExecutor(t *testing.T) *executor {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), mgTunerCaptureFixtureSQL); err != nil {
		t.Fatal(err)
	}
	return w
}

func TestMGTunerNativeStatementCapture(t *testing.T) {
	defs := mgTunerDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	w := mgTunerCaptureExecutor(t)
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(mgTunerCaptureFixtureSQL))
	var before, after int64
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	g, err := cachedCapture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Closed MG tuner point and bounded-stream definitions only: allocation PK, kind/resource UNIQUE, current head channel PK, both held-state index streams, enabled reservation window and exact tuple CAS. No authority, reservation selection, admission, receipt, physical release or performance qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	// The INSERT destination is audited with its complete FK set; read and CAS
	// definitions contribute no external key mutation.
	if len(g.Manifest) != 8 || len(g.Plans) != 8 || g.ForeignKeys == nil {
		t.Fatal("capture inventory", len(g.Manifest), len(g.Plans), g.ForeignKeys == nil)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&after); err != nil || after != before {
		t.Fatal("Capture executed effects", before, after, err)
	}
	actual, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-tuner-plans.json.gz"
	if *recordMGTunerPlans {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		if _, err := gz.Write(actual); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(expected))
	if err != nil {
		t.Fatal(err)
	}
	expected, err = io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := g.Compare(expected); err != nil {
		t.Fatal(err)
	}
}

// Every acquire/renew and retire/reconcile family member is SEARCH-only with no
// temporary B-tree before and after ANALYZE. This is component evidence, not the
// Capture receipt above.
func TestMGTunerNativePlans(t *testing.T) {
	w := mgTunerCaptureExecutor(t)
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
		defs := append(mgTunerDefinitions(), mgTunerRetireDefinitions()...)
		for _, d := range defs {
			if issues := plan.LintSQLOn(t.Context(), conn, d.SQL); len(issues) > 0 {
				t.Fatal(d.ID, issues)
			}
			if d.ID == mgTunerInsert {
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
// widening the shared registry.
func TestMGTunerDescriptors(t *testing.T) {
	got := mgTunerDescriptors()
	want := []Descriptor{{814, "mg.tuner.acquire", Normal}, {815, "mg.tuner.renew", Normal}}
	if len(got) != len(want) {
		t.Fatal(got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatal(i, got[i], want[i])
		}
	}
	if _, err := Describe(MGAcquireAllocation); err != nil {
		t.Fatal(err)
	}
	if _, err := Describe(MGRenewAllocation); err != nil {
		t.Fatal(err)
	}
}
