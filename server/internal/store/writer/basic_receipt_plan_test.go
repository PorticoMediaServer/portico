package writer

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

var recordBasicReceiptPlans = flag.Bool("record-basic-receipt-plans", false, "deliberately record post-CHANGE011 Basic receipt component plans after the capture hold is released")

func TestBasicReceiptStatementPlans(t *testing.T) {
	actual, fk := captureBasicReceiptPlans(t)
	path := filepath.Join("testdata", "scan-basic-receipt.json")
	fkPath := filepath.Join("testdata", "scan-basic-receipt-fk.json.gz")
	if *recordBasicReceiptPlans {
		if err := os.WriteFile(path, actual, 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fkPath, fk, 0644); err != nil {
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
	if expected, err := os.ReadFile(fkPath); err != nil || !bytes.Equal(expected, fk) {
		t.Fatal("raw independent FK capture parity", err)
	}
}

// Native component population only: the SAME Stage body saves the original
// canonical request/result digests with ZERO Outcome. No accepting dispatcher,
// public result, source permission, resource profile or hold is qualified here.
func captureBasicReceiptPlans(t *testing.T) ([]byte, []byte) {
	t.Helper()
	// Canonical private phase0/SCHEMA.sql at d582f7950907817d3b91e18be3de87c5d31dc407,
	// INT-CHANGE-011.md candidate. SUP holds new captures until embed #1382
	// merges and releases them; this gate must never relabel embedded010 as011.
	const schema011 = "14f7b58ffe4b7c2ad1682dbb6ba52c028fca956875b16de8e52479b198d793d2"
	ddl := sha256.Sum256([]byte(schema.SQL()))
	if schema.SHA256 != schema011 || hex.EncodeToString(ddl[:]) != schema011 {
		t.Fatal("Basic receipt capture held: requires exact CHANGE011 embed and SUP capture release", schema.SHA256)
	}
	journal := basicReceiptPlanJournal(t)
	defs := append(basicReceiptDefinitions(), basicCommandStateDefinitions()...)
	cohorts := 0
	wantIDs := map[stmt.ID]int{basicReceiptScope: 2, basicReceiptIdentity: 2, basicReceiptHeaderLengths: 2, basicReceiptHeader: 2, basicControlChange: 1}
	for _, d := range defs {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Waivers) != 0 || wantIDs[d.ID] != len(d.Cohorts) {
			t.Fatal("closed hot unwaived receipt/state definition changed", d.ID)
		}
		delete(wantIDs, d.ID)
		cohorts += len(d.Cohorts)
	}
	if len(defs) != 5 || cohorts != 9 || len(wantIDs) != 0 {
		t.Fatal("five-definition/nine-cohort inventory changed", len(defs), cohorts)
	}
	w, source, original, identity := basicControlFixture(t)
	// The actual StageFact command requires the complete FIRST family mask.
	// Retain all other owner fixture facts, including CJR phase0/jobR1.
	if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_info_changes SET family_mask=511 WHERE id=51`); err != nil {
		t.Fatal(err)
	}
	command, err := newBasicStageCommand(identity, basicStageRequest{original: original, purpose: BasicPurposeStage, operation: basicStageFactInput, fact: source, changeRevision: 1})
	if err != nil {
		t.Fatal(err)
	}
	requestDigest, err := canonicalRequestDigest(BasicStage, 1, identity, command.request)
	if err != nil || requestDigest != command.digest || requestDigest == (RequestDigest{}) {
		t.Fatal("original canonical Stage request", err)
	}
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	// Capture the full required effects subset and the four history points ONCE
	// on FULL. basicHandlerTurn receives this real registry; it must not try a
	// second startupCapture after w.begin changes the connection to NORMAL.
	startupDefs := append(basicStageCommandDefinitions(command.request), basicReceiptDefinitions()...)
	startupCatalogue, err := stmt.Describe(startupDefs)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := w.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	full, err := sqliteconn.Assert(t.Context(), conn, w.config)
	if err != nil || full.Synchronous != 2 {
		t.Fatal("FULL startup boundary", full, err)
	}
	var startupBefore, startupAfter int64
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&startupBefore); err != nil {
		t.Fatal(err)
	}
	startup, err := plan.Capture(t.Context(), conn, startupCatalogue, plan.Fixture{ConnectionPolicy: w.config, SHA256: journal, Statistics: plan.Unanalyzed,
		Implementations: "frozen actual Basic StageFact fixture; FULL startup registry only; synthetic component forecasts, no command/resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&startupAfter); err != nil || startupAfter != startupBefore {
		t.Fatal("startup Capture changed owner population", startupBefore, startupAfter, err)
	}
	registry, err := stmt.New(startupDefs, startup.Receipt())
	if err != nil {
		t.Fatal("actual FULL startup registry", err)
	}
	// Subset registration uses the SAME actual opaque receipt and unchanged
	// definitions. Post-body Capture will mint its own NORMAL environment.
	target, err := stmt.New(defs, startup.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	turn := basicHandlerTurn(t, w, BasicStage, startupDefs, registry)
	t.Cleanup(func() { _ = turn.closeRows() })
	tx := turn.transaction
	config := w.config
	config.Synchronous = sqliteconn.Normal
	normal, err := sqliteconn.Assert(tx.ctx, tx.conn, config)
	if err != nil || normal.Synchronous != 1 {
		t.Fatal("NORMAL acquired Stage owner turn", normal, err)
	}
	t.Logf("Basic receipt physical acquisition FULL=%+v NORMAL=%+v", full, normal)
	staged, err := applyBasicStage(tx.ctx, turn, command)
	if err != nil || staged.Outcome != (ReceiptOutcome{}) || staged.Purpose != BasicPurposeStage ||
		staged.RunID != source.runID || staged.AcceptedRunRevision != 42 || staged.AppliedMS < 0 ||
		staged.SourceID != source.sourceID || staged.SourceIncarnation != source.incarnation || staged.SourceRevision != source.objectRevision ||
		staged.ChangeID != source.changeID || staged.ExpectedEpoch != source.expectedEpoch ||
		staged.Continuation != (BasicContinuation{}) || staged.ResultCount != 0 || staged.Members == nil || len(staged.Members) != 0 {
		t.Fatal("same native Stage body did not produce the complete ZERO Outcome population", staged, err)
	}
	resultDigest, err := basicResultDigest(identity, command.digest, staged)
	if err != nil || resultDigest != staged.Digest || resultDigest == (ResultDigest{}) {
		t.Fatal("same W canonical saved-result codec", err)
	}
	var exact bool
	if err := tx.conn.QueryRowContext(tx.ctx, `SELECT
 (SELECT job_revision=42 AND last_command_sequence=19 AND acknowledged_command_sequence=18
  AND discovered=59 AND analyzed=43 AND warnings=47 FROM inventory_runs WHERE job_id='basic-run')
 AND (SELECT processed=67 FROM jobs WHERE id='basic-run')
 AND (SELECT phase=0 AND job_revision=1 AND input_revision=7 AND family_mask=511 FROM catalog_info_changes WHERE id=51)
 AND (SELECT rows_staged=1 AND sealed=0 FROM catalog_info_input_families WHERE change_id=51 AND family=1)
 AND (SELECT count(*)=1 FROM catalog_info_new_stream_facts WHERE change_id=51)
 AND (SELECT count(*)=0 FROM scan_command_results)
 AND (SELECT info_epoch=17 AND info_publication_pending=0 FROM catalog_assets WHERE id=41)`).Scan(&exact); err != nil || !exact {
		t.Fatal("native Stage population mixed run/CJR revisions or changed progress/publication", exact, err)
	}
	// Execute every unchanged source-owned cohort via the existing turn, so
	// saved_stage is a real exact native row and missing is actual EOF.
	for _, d := range defs {
		for _, cohort := range d.Cohorts {
			rows, err := turn.query(tx.ctx, turn.steps[d.ID].statementStep, cohort.Bindings...)
			if err != nil {
				t.Fatal(d.ID, cohort.Name, err)
			}
			var want []any
			switch d.ID {
			case basicReceiptScope:
				want = []any{source.runID}
			case basicReceiptIdentity:
				want = []any{identity.OperationID[:], command.digest[:], string(BasicPurposeStage), int64(0), staged.Digest[:]}
			case basicReceiptHeaderLengths:
				want = []any{int64(len(source.sourceID)), int64(len(source.incarnation)), int64(len(source.objectRevision)), int64(0)}
			case basicReceiptHeader:
				want = []any{int64(42), staged.AppliedMS, source.sourceID, source.incarnation, source.objectRevision, "", source.changeID, source.expectedEpoch}
			case basicControlChange:
				want = []any{int64(1)} // CJR job revision, independently of runR42
			}
			if cohort.Name == "missing" {
				if rows.Next() {
					t.Fatal("missing cohort has a native row", d.ID)
				}
			} else {
				values, dest := make([]any, len(want)), make([]any, len(want))
				for i := range values {
					dest[i] = &values[i]
				}
				if !rows.Next() {
					t.Fatal("saved native cohort is empty", d.ID, cohort.Name, rows.Err())
				}
				if err := rows.Scan(dest...); err != nil || !reflect.DeepEqual(values, want) || rows.Next() {
					t.Fatal("exact same-body cohort projection", d.ID, cohort.Name, values, want, err)
				}
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	var before, after int64
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&before); err != nil {
		t.Fatal(err)
	}
	// Read-only targets do not implicitly capture FK probes. Independently
	// acquire the complete real audit, including all 671 FKs / 2013 probes.
	fk, err := plan.ForeignKeys(tx.ctx, tx.conn, config)
	if err != nil || len(fk.Plans) != 671 {
		t.Fatal("independent complete FK acquisition", len(fk.Plans), err)
	}
	fkJSON, err := json.MarshalIndent(fk, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := plan.CompareExactJSON(readScanFreshFK(t, filepath.Join("testdata", "scan-inventory-accept-fk.json.gz")), fkJSON); err != nil {
		t.Fatal("independent audit differs from the merged CHANGE011 FK reference", err)
	}
	g, err := plan.Capture(tx.ctx, tx.conn, target, plan.Fixture{ConnectionPolicy: config, SHA256: journal, Statistics: plan.Unanalyzed,
		Implementations: "frozen basicControlFixture + actual newBasicStageCommand/applyBasicStage; SAME native StageFact body; saved_stage and missing real populations; CJR phase0/jobR1, source.Scope runR42; ZERO Outcome; synthetic component forecasts, no accepting/public/command/resource qualification"}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stmt.New(defs, g.Receipt()); err != nil {
		t.Fatal("actual exact five-definition NORMAL receipt", err)
	}
	if !reflect.DeepEqual(g.Manifest, target.Manifest()) || len(g.Manifest) != 5 || len(g.Plans) != 9 || g.ForeignKeys != nil {
		t.Fatal("complete unchanged five-definition/nine-cohort target manifest", len(g.Manifest), len(g.Plans))
	}
	vm, searches := 0, 0
	for _, p := range g.Plans {
		if len(p.Explain.Values) == 0 || p.Insert != nil || len(p.QueryPlan.Values) != 1 {
			t.Fatal("missing VM or native point plan", p.ID, p.Cohort)
		}
		vm += len(p.Explain.Values)
		for _, row := range p.QueryPlan.Values {
			if len(row) != 4 || !strings.HasPrefix(row[3].(string), "SEARCH ") {
				t.Fatal("non-SEARCH receipt/state path", p.ID, p.Cohort, row)
			}
			searches++
		}
	}
	if err := tx.conn.QueryRowContext(tx.ctx, "SELECT total_changes()").Scan(&after); err != nil || before != after {
		t.Fatal("Capture/FK audit changed same-body population", before, after, err)
	}
	// Only the private physical component commit. Do not invoke the accepting
	// dispatcher, publish an Outcome or claim command/terminal qualification.
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if physical, err := tx.commit(); err != nil || physical.state != committed {
		t.Fatal("component physical commit", physical, err)
	}
	actual, err := json.MarshalIndent(g, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	z := gzip.NewWriter(&compressed)
	if _, err := z.Write(fkJSON); err != nil {
		t.Fatal(err)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("5IDs/9cohorts/%dVM/%dSEARCH/671FK2013probes; source/seed journal %s; component only", vm, searches, journal)
	return append(actual, '\n'), compressed.Bytes()
}

// End of the self-journaled population span. Its digest is outside that span.
func basicReceiptPlanJournal(t *testing.T) string {
	t.Helper()
	parts := []struct{ file, start, end, sha string }{
		{"basic_control_test.go", "func basicControlFixture(", "func basicControlTurn(", "cb76a1fe1228972c7fe89db4533ebe4ebf748f6f6dc03794d21d188e85228071"},
		{"basic_stage_test.go", "func basicStreamFixture(", "func basicStreamComponentTurn(", "bd09ee39917e1a6009bcc97fcedfdceac2c0acdeb538bf7b021c22e5b6721ac5"},
		{"transaction_test.go", "func testExecutor(", "func mutateFixture(", "ba118a0ce3ca330ab64370748dbf063341f83f01523c97860f89ce1161243d31"},
		{"basic_handlers_test.go", "func basicHandlerTurn(", "func basicHandlerCommit(", "183ade3953741ba619e13d1ef868579fa805e0461439492b04450a6fd089ded8"},
		{"governor_turn_test.go", "type governedFixtureProfile struct", "func TestGovernedTurnNativeReturnAndWALRefresh(", "0e5964538f46de1d26bc12d2b4b6274449d99708809587cce97043880dc7133f"},
		{"schema_fixture_test.go", "", "", "9bd003c6f7cbcc4c9e90f7f9cdc46746b048f474c8ca371b837950a0b1b8366a"},
		{"qualification_population_test.go", "", "", "4806a0afa77e1d8aa45ee77ec0aff355920970ae7e387d95a3f78d48c775436a"},
		{"../internal/schematest/fixture.go", "", "", "5b72c48d9358cf40b25dad059bad2e4d8ad7534261a793729a1d82d5457bc9c3"},
		{"basic_stage.go", "", "", "89f3e1171aaea3bf9ff5265e8cdf1e478df9630354ce9d806ca97464870300c7"},
		{"basic_control.go", "", "", "57181bc88298c11f3fa924b986b6a03f9ed8c60172e0bc79775823ad58db62d5"},
		{"basic_control_statements.go", "", "", "cae20d889dc5a37925dedf58142fa872ab6108ac38745cd0b6320c340eb70b4a"},
		{"basic_original.go", "", "", "5015f3cc2fafad60e60b004129e72a89a77037d1168076f02071a8f94812c3da"},
		{"basic_commands.go", "", "", "07d9124493e95819af03f14c3a96ee790eef7fa0bda6a6d94cc2c42750a257e9"},
		{"basic_handlers.go", "", "", "7835f8503c0d2d231b8870cd85a983b75c9d1dc952b9d60858cd8b37e98f16ad"},
		{"basic_resources.go", "", "", "883530175102d3eb785560f2224553f7bc1b7299b885798c00b11d151dab7b7c"},
		{"basic_dispatch_composition.go", "", "", "794f98f4712e45bf8342d2c9c9d5d613b48cafe3341731b03bcf1d640c81f71b"},
		{"basic_command_state.go", "", "", "41ecb872131946e102e873cdba1a43946444940ac4ae6a7e1a5bc89fe3ea01a7"},
		{"basic_receipt_statements.go", "", "", "0c7deec01fa89dcd38c634e8f7fae90343714bdaceedbb1a7aa1d3f7986c62f2"},
		{"basic_result_digest.go", "", "", "0c9159e2743818d5be65a455670f72d98e74d22eb38e243da45b4f3d13e91f23"},
		{"request_digest.go", "", "", "d865f56b333cf846d10c804af56a00e14f5a7d4f058fec6349708c96557e9dc8"},
		{"result_digest.go", "", "", "c6870c3a78172aeb84ec5bae3349cd48983c52fc54d7f3e90520eec8603d8c65"},
		{"basic_contract.go", "", "", "2959bf77a76f5de8f7e4c9c23e0616deb07c0f130025f0994eb8a23b94283fc2"},
		{"scan_types.go", "type inventoryScope struct", "type inventoryDirectory struct", "7e1efe7aeebaa93356cde9c8fc560668bf3644299f98bdff6fd3ed6490ff01dd"},
		{"scan_fresh_plan_test.go", "func readScanFreshFK(", "", "b4f8eb0fceff0e1ca7a8bc80abada2330564d87efc8702fa38368fbe76fc3418"},
		{"basic_receipt_plan_test.go", "func captureBasicReceiptPlans(", "// End of the self-journaled population span.", "771d56a8eb0311b66d71511a03c229c56ff4490f72ffb474d8b348e23edfac79"},
	}
	h := sha256.New()
	for _, part := range parts {
		data, err := os.ReadFile(part.file)
		if err != nil {
			t.Fatal(err)
		}
		if part.start != "" {
			start, end := bytes.Index(data, []byte(part.start)), len(data)
			if part.end != "" {
				end = bytes.Index(data, []byte(part.end))
			}
			if start < 0 || end <= start {
				t.Fatal("missing frozen fixture source span", part.file)
			}
			data = data[start:end]
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != part.sha {
			t.Fatal("frozen Basic receipt source/seed changed; review before recording", part.file)
		}
		name := part.file + ":" + part.start
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(name))))
		h.Write([]byte(name))
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(data))))
		h.Write(data)
	}
	return hex.EncodeToString(h.Sum(nil))
}
