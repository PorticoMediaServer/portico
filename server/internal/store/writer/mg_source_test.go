package writer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// Canonical existing source fields only: this slice needs no G DDL overlay.
const mgSourceFixtureSQL = `INSERT INTO live_source_identities(id) VALUES('source');
INSERT INTO live_generations(id,source_id,name,tuner_count,channel_count,programme_count,created_at) VALUES('old','source','Old',1,2,9,'before'),('new','source','Guide',1,2,9,'2026-11-07T00:00:00.000Z');
INSERT INTO live_sources(id,name,revision,state,tuner_count,active_generation,published_at) VALUES('source','Old',7,'active',1,'old','before');`

func mgSourceTestExecutor(t *testing.T) *executor {
	t.Helper()
	w, _ := testCanonicalExecutor(t)
	if _, err := w.db.ExecContext(t.Context(), mgSourceFixtureSQL); err != nil {
		t.Fatal(err)
	}
	return w
}
func mgSourceTestTurn(t *testing.T, w *executor, purpose CommandID, custom ...[]stmt.Definition) *commandTurn {
	t.Helper()
	clock := time.Now().Add(time.Hour)
	w.now = func() time.Time { return clock } // component, no timing claim
	defs := mgSourceDefinitions()
	if len(custom) > 0 {
		defs = custom[0]
	}
	r, err := stmttest.New(t, defs)
	if err != nil {
		t.Fatal(err)
	}
	recipe := turnRecipe{limit: Work{1000, 1000000, 1000, 8 << 20, 25 * time.Millisecond}, setup: Work{8, 100, 8, 4096, time.Millisecond}, terminal: Work{8, 100, 8, 4096, time.Millisecond}}
	tx, err := w.begin(t.Context(), purpose, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	var steps []statementStep
	for _, d := range defs {
		steps = append(steps, statementStep{d.ID, Work{16, 10000, 32, 65536, time.Microsecond}})
	}
	turn, err := newCommandTurn(tx, r, steps)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = turn.closeRows() })
	return turn
}
func mgSourceFixture(t *testing.T, purpose CommandID) (*executor, *commandTurn, mgSourceFacts) {
	t.Helper()
	w := mgSourceTestExecutor(t)
	turn := mgSourceTestTurn(t, w, purpose)
	s, err := mgReadSource(t.Context(), turn, "source")
	if err != nil {
		t.Fatal(err)
	}
	return w, turn, s
}
func TestMGSourceConfigurationCapacityAndHeadFence(t *testing.T) {
	for _, test := range []struct {
		name            string
		physical, limit int64
		use             bool
		want            int64
	}{{"unknown", 0, 0, true, 0}, {"limited", 4, 2, true, 2}, {"discovered", 4, 0, true, 4}, {"ignored", 4, 0, false, 0}, {"configured", 0, 3, false, 3}} {
		t.Run(test.name, func(t *testing.T) {
			_, turn, before := mgSourceFixture(t, MGConfigureTuner)
			out, err := mgConfigureSource(t.Context(), turn, before, mgSourceConfiguration{"Configured", test.physical, test.limit, test.use})
			if err != nil || out.source.tuners != test.want || out.source.revision != 8 || out.source.name != "Configured" || out.source.generation != "old" || out.source.viewerAccess != "owner-only" {
				t.Fatal(out, err)
			}
			if out, err := mgConfigureSource(t.Context(), turn, before, mgSourceConfiguration{"Stale", 1, 1, true}); !errors.Is(err, ErrConflict) || out != (mgSourceResult{}) {
				t.Fatal("stale configuration", out, err)
			}
		})
	}
}
func TestMGSourceSecurityPurposeAndNativeFullPolicy(t *testing.T) {
	for _, purpose := range []CommandID{MGSetSourceEnabled, MGSetSourceViewerAccess, MGRetireSource} {
		t.Run(fmt.Sprint(purpose), func(t *testing.T) {
			w, turn, before := mgSourceFixture(t, purpose)
			if turn.transaction.settings.Synchronous != int(sqliteconn.Full) || turn.transaction.descriptor.Durability != Full {
				t.Fatal("security was not FULL")
			}
			var result mgSourceResult
			var err error
			switch purpose {
			case MGSetSourceEnabled:
				result, err = mgSetSourceEnabled(t.Context(), turn, before, false)
			case MGSetSourceViewerAccess:
				result, err = mgSetSourceAccess(t.Context(), turn, before, "server-members")
			case MGRetireSource:
				result, err = mgRetireSource(t.Context(), turn, before, "retire-request")
			}
			if err != nil || result.source.revision != 8 || result.source.generation != "old" {
				t.Fatal(result, err)
			}
			if purpose == MGRetireSource && (result.source.removed != 1 || result.source.state != "disabled" || result.source.removalRevision.Int64 != 8) {
				t.Fatal(result)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
			if _, err := turn.transaction.commit(); err != nil {
				t.Fatal(err)
			}
			var generations int64
			if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM live_generations WHERE source_id='source'`).Scan(&generations); err != nil || generations != 2 {
				t.Fatal("configuration retired generation custody", generations, err)
			}
		})
	}
	_, turn, before := mgSourceFixture(t, MGConfigureTuner)
	if out, err := mgSetSourceAccess(t.Context(), turn, before, "server-members"); !errors.Is(err, ErrInvalid) || out != (mgSourceResult{}) {
		t.Fatal("NORMAL frame performed security write", out, err)
	}
	if out, err := mgRetireSource(t.Context(), turn, before, "retire"); !errors.Is(err, ErrInvalid) || out != (mgSourceResult{}) {
		t.Fatal("wrong purpose retired source", out, err)
	}
}
func TestMGSourceSettingsFailureRollsBackWholeFrame(t *testing.T) {
	w := mgSourceTestExecutor(t)
	defs := mgSourceDefinitions()
	for j := range defs {
		if defs[j].ID == mgSourceUpdate {
			defs[j].SQL = strings.Replace(defs[j].SQL, "revision=? AND state=?", "revision=? AND revision=-1 AND state=?", 1)
		}
	}
	turn := mgSourceTestTurn(t, w, MGConfigureTuner, defs)
	before, err := mgReadSource(t.Context(), turn, "source")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := mgConfigureSource(t.Context(), turn, before, mgSourceConfiguration{"New", 4, 2, true}); !errors.Is(err, ErrConflict) || out != (mgSourceResult{}) {
		t.Fatal(out, err)
	}
	var prefix int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT settings_physical_count FROM live_source_identities WHERE id='source'`).Scan(&prefix); err != nil || prefix != 4 {
		t.Fatal("control missed partial prefix", prefix, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT settings_physical_count FROM live_source_identities WHERE id='source'`).Scan(&prefix); err != nil || prefix != 0 {
		t.Fatal("partial settings escaped", prefix, err)
	}
}
func TestMGSourceRemovedAndCancelledRefuseNewEffects(t *testing.T) {
	_, turn, before := mgSourceFixture(t, MGRetireSource)
	retired, err := mgRetireSource(t.Context(), turn, before, "removed")
	if err != nil {
		t.Fatal(err)
	}
	if out, err := mgRetireSource(t.Context(), turn, retired.source, "removed-again"); !errors.Is(err, ErrConflict) || out != (mgSourceResult{}) {
		t.Fatal("retired source revived", out, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if out, err := mgReadSource(ctx, turn, "source"); !errors.Is(err, context.Canceled) || out != (mgSourceFacts{}) {
		t.Fatal("cancelled facts leaked", out, err)
	}
}

var recordMGSourcePlans = flag.Bool("record-mg-source-plans", false, "deliberately record closed source point plans")

func TestMGSourceNativeStatementCapture(t *testing.T) {
	w := mgSourceTestExecutor(t)
	defs := mgSourceDefinitions()
	catalogue, err := stmt.Describe(defs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(mgSourceFixtureSQL))
	g, err := plan.Capture(t.Context(), conn, catalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: hex.EncodeToString(digest[:]), Statistics: plan.Unanalyzed, Implementations: "Existing canonical source/identity fixture; point component facts only; not authority, lifetime, admission, COMMIT or qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(g.Plans) != 5 {
		t.Fatal("source plans", len(g.Plans))
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal(err)
	}
	actual, err := json.MarshalIndent(g.Plans, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := "testdata/mg-source-plans.json"
	if *recordMGSourcePlans {
		if err := os.WriteFile(path, append(actual, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}
	expected, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareJSON(expected, actual); err != nil {
		t.Fatal(err)
	}
}

// Native NORMAL observation plus a mislabeled FULL purpose must be refused.
// This tests frame-policy binding rather than merely a wrong command name.
func TestMGSourceSecurityRejectsNativeNormalFrame(t *testing.T) {
	for _, purpose := range []CommandID{MGSetSourceEnabled, MGSetSourceViewerAccess, MGRetireSource} {
		t.Run(fmt.Sprint(purpose), func(t *testing.T) {
			_, turn, before := mgSourceFixture(t, MGConfigureTuner)
			var native int
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `PRAGMA synchronous`).Scan(&native); err != nil {
				t.Fatal(err)
			}
			if native != int(sqliteconn.Normal) || turn.transaction.settings.Synchronous != native || !turn.transaction.policyVerified {
				t.Fatal("control did not observe native NORMAL", native, turn.transaction.settings)
			}
			descriptor, err := Describe(purpose)
			if err != nil {
				t.Fatal(err)
			}
			turn.transaction.descriptor = descriptor // adversarial private frame-assembly control
			var changesBefore, changesAfter int64
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changesBefore); err != nil {
				t.Fatal(err)
			}
			var out mgSourceResult
			switch purpose {
			case MGSetSourceEnabled:
				out, err = mgSetSourceEnabled(t.Context(), turn, before, false)
			case MGSetSourceViewerAccess:
				out, err = mgSetSourceAccess(t.Context(), turn, before, "server-members")
			case MGRetireSource:
				out, err = mgRetireSource(t.Context(), turn, before, "retire")
			}
			if !errors.Is(err, ErrInvalid) || out != (mgSourceResult{}) {
				t.Fatal("native NORMAL security write accepted", out, err)
			}
			if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT total_changes()`).Scan(&changesAfter); err != nil || changesBefore != changesAfter {
				t.Fatal("refusal performed SQL effects", changesBefore, changesAfter, err)
			}
		})
	}
}
