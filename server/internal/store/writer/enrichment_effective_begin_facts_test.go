package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// A fact population, not a valid producer entry or a verified command result.
// In particular, this phase is deliberately NOT admitted by any domain handler,
// and resultDigest is synthetic until W's reviewed result helper is on main.
func effectiveBeginNativePopulation(t *testing.T, w *executor) (effectiveBeginOwner, effectiveBeginReceipt, effectiveBeginEntity) {
	t.Helper()
	var inc, entityInc, policyInc, operation [16]byte
	var request RequestDigest
	var result [32]byte
	for i := range inc {
		inc[i], entityInc[i], policyInc[i], operation[i] = byte(16+i), byte(48+i), byte(80+i), byte(112+i)
	}
	for i := range request {
		request[i], result[i] = byte(144+i), byte(192+i)
	}
	for _, q := range []string{
		`INSERT INTO libraries(id,name,kind,root) VALUES('library-native','Library','movie','/media')`,
		`INSERT INTO catalog_libraries(id,library_id,root) VALUES(7,'library-native','/namespace')`,
		`INSERT INTO catalog_kinds(id,name,playable,container,searchable,browsable,listening) VALUES(1,'movie',1,0,1,1,0)`,
		`INSERT INTO metadata_providers(code,adapter,enabled) VALUES('fixture','fixture-adapter',1)`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_subjects(id,public_id,library_id,kind) VALUES(101,?,7,1)`, operation[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO catalog_entities(
 id,public_id,library_id,kind,title,sort_key,sort_head,sort_key_keep_articles,sort_head_keep_articles,
 random_key,metadata_incarnation,artwork_incarnation,revision,source_revision,identity_revision,
 metadata_revision,metadata_language,credit_owner_revision,credit_owner_next_revision,credit_owner_locked)
 VALUES(101,?,7,1,'Native title','native','n','native','n',0,?,?,203,13,17,19,'en-CA',23,29,1)`, operation[:], entityInc[:], policyInc[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO metadata_provider_operations(
 id,entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,
 source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,descriptive_revision,
 policy_incarnation,policy_revision,consent_revision,algorithm,refresh_mode,query_title,query_artist,query_year,
 query_digest,input_digest,selected_id,manual,review_search,qualification,created_at,command_incarnation,
 last_command_sequence,acknowledged_command_sequence,page,after_item,source_content_revision)
 VALUES('operation-one',101,'fixture','unadmitted-fixture-phase','staged',31,'native-lease',
 '2026-11-05T12:00:00.000000000Z',?,13,17,19,43,47,53,?,37,41,'native-algorithm','fill_missing',
 'Native query','',2001,'native-query-digest','native-input-digest','opaque:selection',1,0,'native-qualification',
 '2026-11-05T10:00:00.000000000Z',?,11,10,59,'opaque-page:61',1)`, entityInc[:], policyInc[:], inc[:]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `UPDATE metadata_provider_operations SET
 query_title='Native query:149',query_artist='Native artist:151',query_digest='native-query-digest:157',
 hint_id='opaque-hint:163',hint_problem='hint-problem:167',qualification='native-qualification:173'
 WHERE id='operation-one'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.db.ExecContext(t.Context(), `INSERT INTO enrichment_command_receipts(
 provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,entity_id,after_item,after_ordinal,publication_id,
 effective_image_id,effective_image_revision,result_count,result_digest)
 VALUES('operation-one',?,11,?,?,'effective_stage',203,1793861096027,101,'historical-page:67',0,NULL,307,409,0,?)`, inc[:], operation[:], request[:], result[:]); err != nil {
		t.Fatal(err)
	}
	owner := effectiveBeginOwner{
		receiptOwnerFacts: receiptOwnerFacts{true, inc, 11, 10},
		entityID:          101, generation: 31, provider: "fixture", phase: "unadmitted-fixture-phase", status: "staged",
		lease: "native-lease", leaseUntil: "2026-11-05T12:00:00.000000000Z",
		entityIncarnation: entityInc, policyIncarnation: policyInc,
		sourceRevision: 13, identityRevision: 17, metadataRevision: 19,
		hierarchyRevision: 43, selectionRevision: 47, descriptiveRevision: 53,
		policyRevision: 37, consentRevision: 41, algorithm: "native-algorithm", refreshMode: "fill_missing",
		inputDigest: "native-input-digest", selectedID: "opaque:selection", page: 59, afterItem: "opaque-page:61",
		queryTitle: "Native query:149", queryArtist: "Native artist:151", queryYear: 2001,
		queryDigest: "native-query-digest:157", manual: 1, reviewSearch: 0,
		hintID: "opaque-hint:163", hintProblem: "hint-problem:167", qualification: "native-qualification:173",
	}
	receipt := effectiveBeginReceipt{
		exists: true, identity: receiptIdentity{CommandIdentity{inc, 11, operation}, request}, kind: "effective_stage",
		acceptedRevision: 203, appliedMS: 1793861096027, entityID: 101, afterItem: "historical-page:67",
		imageID: sql.NullInt64{Int64: 307, Valid: true}, imageRevision: sql.NullInt64{Int64: 409, Valid: true},
		resultDigest: result,
	}
	entity := effectiveBeginEntity{
		id: 101, libraryID: 7, kind: 1, revision: 203, incarnation: entityInc,
		sourceRevision: 13, identityRevision: 17, metadataRevision: 19,
		language: "en-CA", ownerRevision: 23, ownerLocked: 1,
	}
	return owner, receipt, entity
}

func effectiveBeginFactTurn(t *testing.T, w *executor) (*commandTurn, map[stmt.ID]statementStep) {
	t.Helper()
	// Component-only read recipe. These virtual forecasts are NOT production
	// admission/HOLD/WAL data, and the frame grants no domain Applied result.
	tx, err := w.begin(t.Context(), CreditStage, testRecipe(), &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	defs := enrichmentEffectiveBeginDefinitions()
	config := w.config
	config.Synchronous = sqliteconn.Normal
	golden := captureEffectiveFixture(t, tx.conn, config, defs)
	registry, err := stmt.New(defs, golden.Receipt())
	if err != nil {
		t.Fatal(err)
	}
	var steps []statementStep
	byID := map[stmt.ID]statementStep{}
	for _, definition := range registry.Manifest() {
		step := statementStep{definition.ID, Work{1, 100, 1, 0, time.Microsecond}}
		steps, byID[step.id] = append(steps, step), step
	}
	turn, err := newCommandTurn(tx, registry, steps)
	if err != nil {
		t.Fatal(err)
	}
	return turn, byID
}

func TestEffectiveBeginNativeFactsKeepEveryCoordinate(t *testing.T) {
	for _, nullable := range []string{"null_publication", "present_empty_zero", "distinct_tv_fields"} {
		t.Run(nullable, func(t *testing.T) {
			w, _, _ := testExecutor(t)
			owner, receipt, entity := effectiveBeginNativePopulation(t, w)
			if nullable == "present_empty_zero" {
				if _, err := w.db.ExecContext(t.Context(), `UPDATE enrichment_command_receipts SET publication_id='',effective_image_id=0,effective_image_revision=0 WHERE provider_operation_id='operation-one'`); err != nil {
					t.Fatal(err)
				}
				receipt.publicationID, receipt.imageID, receipt.imageRevision = sql.NullString{String: "", Valid: true}, sql.NullInt64{Valid: true}, sql.NullInt64{Valid: true}
			}
			if nullable == "distinct_tv_fields" {
				for i := range entity.tvIncarnation.value {
					entity.tvIncarnation.value[i] = byte(224 + i)
				}
				entity.tvIncarnation.present = true
				entity.tvSourceRevision = sql.NullInt64{Int64: 71, Valid: true}
				entity.tvHierarchyRevision = sql.NullInt64{Int64: 73, Valid: true}
				entity.tvSelectionRevision = sql.NullInt64{Int64: 79, Valid: true}
				entity.tvDescriptiveRevision = sql.NullInt64{Int64: 83, Valid: true}
				if _, err := w.db.ExecContext(t.Context(), `UPDATE catalog_entities SET tv_incarnation=?,tv_source_revision=71,tv_hierarchy_revision=73,tv_selection_revision=79,tv_descriptive_revision=83 WHERE id=101`, entity.tvIncarnation.value[:]); err != nil {
					t.Fatal(err)
				}
			}
			turn, steps := effectiveBeginFactTurn(t, w)
			gotOwner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], "operation-one")
			if err != nil || !reflect.DeepEqual(gotOwner, owner) {
				t.Fatalf("native owner differs: %+v, %v", gotOwner, err)
			}
			gotReceipt, err := effectiveBeginReceiptFacts(t.Context(), turn, steps[enrichmentEffectiveReceipt], "operation-one", receipt.identity.command)
			if err != nil || !reflect.DeepEqual(gotReceipt, receipt) {
				t.Fatalf("historical receipt differs: %+v, %v", gotReceipt, err)
			}
			gotEntity, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], 101)
			if err != nil || !reflect.DeepEqual(gotEntity, entity) {
				t.Fatalf("current entity differs: %+v, %v", gotEntity, err)
			}
			if err := turn.closeRows(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestEffectiveBeginNativeFactsMissingAndRefused(t *testing.T) {
	w, _, _ := testExecutor(t)
	_, receipt, _ := effectiveBeginNativePopulation(t, w)
	turn, steps := effectiveBeginFactTurn(t, w)
	owner, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveOwner], "operation-missing")
	if err != nil || owner.exists {
		t.Fatalf("missing owner: %+v %v", owner, err)
	}
	identity := receipt.identity.command
	identity.Sequence++
	missing, err := effectiveBeginReceiptFacts(t.Context(), turn, steps[enrichmentEffectiveReceipt], "operation-one", identity)
	if err != nil || missing.exists {
		t.Fatalf("missing receipt: %+v %v", missing, err)
	}
	if _, err := effectiveBeginEntityFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], 102); !errors.Is(err, sql.ErrNoRows) {
		t.Fatal("missing entity", err)
	}
	if _, err := effectiveBeginOwnerFacts(t.Context(), turn, steps[enrichmentEffectiveEntity], "operation-one"); !errors.Is(err, ErrInvalid) {
		t.Fatal("cross-statement fact route", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := effectiveBeginOwnerFacts(ctx, turn, steps[enrichmentEffectiveOwner], "operation-one"); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-SQL cancellation", err)
	}
}

func TestEffectiveBeginFactPlansUseActualNativeKeys(t *testing.T) {
	w, _, _, _ := effectiveBeginWholeFixture(t)
	turn, _ := effectiveBeginWholeTurn(t, w)
	data, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(data)
	if err != nil {
		t.Fatal(err)
	}
	config := w.config
	config.Synchronous = sqliteconn.Normal
	// Immutable source/fixture identity for this component capture; no golden
	// recording or accepting statement executes through plan.Capture.
	fixtureSource, err := os.ReadFile("enrichment_effective_begin_handler_test.go")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(fixtureSource)
	var changesBefore, changesAfter int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&changesBefore); err != nil {
		t.Fatal(err)
	}
	golden, err := cachedCapture(t.Context(), turn.transaction.conn, turn.registry, plan.Fixture{
		ConnectionPolicy: config, SHA256: hex.EncodeToString(sum[:]), Statistics: plan.Unanalyzed,
		Implementations: "bundled-native-facts-only",
	}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if len(golden.Plans) != 24 || len(golden.Manifest) != 21 {
		t.Fatal("incomplete native fact capture", len(golden.Plans), len(golden.Manifest))
	}
	if err := turn.transaction.conn.QueryRowContext(t.Context(), "SELECT total_changes()").Scan(&changesAfter); err != nil || changesAfter != changesBefore {
		t.Fatal("plan capture executed effects", changesBefore, changesAfter, err)
	}
	t.Logf("native fact plans: %d cohorts; asserted FK=%d synchronous=%d", len(golden.Plans), golden.Environment.Connection.ForeignKeys, golden.Environment.Connection.Synchronous)
	for _, captured := range golden.Plans {
		t.Logf("static native plan %s/%s: %d EXPLAIN instructions (not runtime VM steps)", captured.ID, captured.Cohort, len(captured.Explain.Values))
	}
}
