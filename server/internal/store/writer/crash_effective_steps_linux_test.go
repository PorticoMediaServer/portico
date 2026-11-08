package writer

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Typed cells preserve native NULL/integer/REAL/TEXT/BLOB across the witness
// pipe. Plain interface{} JSON would silently turn integers/blobs into floats/TEXT.
type effectiveRawCell struct {
	Kind string
	Int  int64
	Real float64
	Text string
	Blob []byte
}

type effectiveRawTable struct {
	Columns []string
	Rows    []map[string]effectiveRawCell
}

type effectiveRawJournal map[string]effectiveRawTable

type effectiveCrashOriginal struct {
	Initial    effectiveRawJournal // immutable native checkpoint BEFORE any accepting call
	Effects    effectiveRawJournal // original raw receipt/image, before its COMMIT
	Retirement [16]byte            // original command INPUT, not a returned/candidate identity
}

var effectiveJournalTables = []string{
	"metadata_provider_operations", "catalog_credit_images", "enrichment_command_receipts",
	"catalog_entities", "catalog_subjects", "catalog_assets", "catalog_asset_links", "inventory_objects",
	"libraries", "catalog_libraries", "library_sources", "metadata_provider_operation_sources",
	"metadata_provider_policy", "metadata_providers", "screen_metadata_consent", "metadata_subject_providers",
	"metadata_provider_subjects", "metadata_evidence", "credit_source_publications", "credit_source_publication_members",
	"catalog_credits", "catalog_credit_labels", "catalog_age_item_counts", "catalog_scalar_contributions", "catalog_asset_contributions",
	"search_revision", "search_generations", "search_publish_jobs", "search_publications", "search_fragments",
}

func effectiveCell(value any) (effectiveRawCell, error) {
	switch v := value.(type) {
	case nil:
		return effectiveRawCell{Kind: "null"}, nil
	case int64:
		return effectiveRawCell{Kind: "integer", Int: v}, nil
	case int: // explicit model literals only; native SQL returns int64
		return effectiveRawCell{Kind: "integer", Int: int64(v)}, nil
	case float64:
		return effectiveRawCell{Kind: "real", Real: v}, nil
	case string:
		return effectiveRawCell{Kind: "text", Text: v}, nil
	case []byte:
		return effectiveRawCell{Kind: "blob", Blob: bytes.Clone(v)}, nil
	default:
		return effectiveRawCell{}, fmt.Errorf("unsupported native journal value %T", value)
	}
}

func effectiveRawRow(columns []string, values []any) (map[string]effectiveRawCell, error) {
	row := map[string]effectiveRawCell{}
	for i, name := range columns {
		cell, err := effectiveCell(values[i])
		if err != nil {
			return nil, err
		}
		row[name] = cell
	}
	return row, nil
}

func sortEffectiveRows(table *effectiveRawTable) {
	sort.Slice(table.Rows, func(i, j int) bool {
		a, _ := json.Marshal(table.Rows[i])
		b, _ := json.Marshal(table.Rows[j])
		return bytes.Compare(a, b) < 0
	})
}

type effectiveJournalReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func readEffectiveJournal(ctx context.Context, db effectiveJournalReader, tables []string) (effectiveRawJournal, error) {
	journal := effectiveRawJournal{}
	for _, name := range tables {
		rows, err := db.QueryContext(ctx, "SELECT * FROM "+name)
		if err != nil {
			return nil, err
		}
		columns, err := rows.Columns()
		if err != nil {
			rows.Close()
			return nil, err
		}
		table := effectiveRawTable{Columns: columns}
		for rows.Next() {
			values, dest := make([]any, len(columns)), make([]any, len(columns))
			for i := range dest {
				dest[i] = &values[i]
			}
			if err = rows.Scan(dest...); err != nil {
				break
			}
			var row map[string]effectiveRawCell
			row, err = effectiveRawRow(columns, values)
			if err != nil {
				break
			}
			table.Rows = append(table.Rows, row)
		}
		err = errors.Join(err, rows.Err(), rows.Close())
		if err != nil {
			return nil, err
		}
		sortEffectiveRows(&table)
		journal[name] = table
	}
	return journal, nil
}

// COMMIT is reached only after the command closes its rows. This independent
// SQL observation uses the ORIGINAL owned native connection, never another pool
// connection or a production classifier/result encoder. These test-oracle reads
// add no writes; their cost is not recipe/resource or latency qualification.
func readEffectiveNativeEffects(ctx context.Context, conn writerDriverConn) (effectiveRawJournal, error) {
	journal := effectiveRawJournal{}
	for _, name := range []string{"catalog_credit_images", "enrichment_command_receipts"} {
		rows, err := conn.QueryContext(ctx, "SELECT * FROM "+name, nil)
		if err != nil {
			return nil, err
		}
		table := effectiveRawTable{Columns: append([]string(nil), rows.Columns()...)}
		for {
			values := make([]driver.Value, len(table.Columns))
			err = rows.Next(values)
			if errors.Is(err, io.EOF) {
				err = nil
				break
			}
			if err != nil {
				break
			}
			untyped := make([]any, len(values))
			for i := range values {
				untyped[i] = values[i]
			}
			var row map[string]effectiveRawCell
			row, err = effectiveRawRow(table.Columns, untyped)
			if err != nil {
				break
			}
			table.Rows = append(table.Rows, row)
		}
		if err = errors.Join(err, rows.Close()); err != nil {
			return nil, err
		}
		sortEffectiveRows(&table)
		journal[name] = table
	}
	return journal, nil
}

func effectiveModelRow(values map[string]any) map[string]effectiveRawCell {
	row := map[string]effectiveRawCell{}
	for k, v := range values {
		cell, err := effectiveCell(v)
		if err != nil {
			panic(err)
		}
		row[k] = cell
	}
	return row
}

func compareEffectiveJournal(want, got effectiveRawJournal) error {
	if len(want) != len(got) {
		return errors.New("effective journal table inventory changed")
	}
	for name, table := range want {
		if !reflect.DeepEqual(table, got[name]) {
			actual := got[name]
			if !reflect.DeepEqual(table.Columns, actual.Columns) || len(table.Rows) != len(actual.Rows) {
				return fmt.Errorf("effective raw %s column/row inventory changed: got %d rows %v, want %d rows %v", name, len(actual.Rows), actual.Columns, len(table.Rows), table.Columns)
			}
			for i, row := range table.Rows {
				for _, column := range table.Columns {
					if !reflect.DeepEqual(row[column], actual.Rows[i][column]) {
						return fmt.Errorf("effective raw %s[%d].%s changed: got %+v, want %+v", name, i, column, actual.Rows[i][column], row[column])
					}
				}
			}
			return fmt.Errorf("effective raw %s has unsupported columns", name)
		}
	}
	return nil
}

// The only accepted changes are the three declared BEGIN effects and ACK's
// owner floor. All other source/serving/search/cohort columns remain byte-exact.
// No production decoder/codec/reducer chooses the expected state or payload.
func effectiveStepExpectation(c *effectiveStageBeginCommand, original *effectiveCrashOriginal, applied, acknowledged bool) (effectiveRawJournal, EffectiveStageBeginResult, error) {
	var zero EffectiveStageBeginResult
	if c == nil || !c.valid || c.identity.Sequence != 1 || c.request.header.expectedCount != 0 || c.request.header.expectedBytes != 0 {
		return nil, zero, errors.New("unsupported effective fixture/input shape")
	}
	if original == nil || len(original.Initial) != len(effectiveJournalTables) || original.Retirement == ([16]byte{}) {
		return nil, zero, errors.New("missing original effective source checkpoint")
	}
	for _, name := range effectiveJournalTables {
		if _, ok := original.Initial[name]; !ok {
			return nil, zero, fmt.Errorf("missing source table %s", name)
		}
	}
	encoded, err := json.Marshal(original.Initial)
	if err != nil {
		return nil, zero, err
	}
	var expected effectiveRawJournal
	if err := json.Unmarshal(encoded, &expected); err != nil {
		return nil, zero, err
	}
	owner := expected["metadata_provider_operations"]
	if len(owner.Rows) != 1 || owner.Rows[0]["last_command_sequence"].Int != 0 || owner.Rows[0]["acknowledged_command_sequence"].Int != 0 || owner.Rows[0]["credit_image_id"].Kind != "null" ||
		len(expected["catalog_credit_images"].Rows) != 0 || len(expected["enrichment_command_receipts"].Rows) != 0 {
		return nil, zero, errors.New("source checkpoint is not the canonical fresh fixture")
	}
	if !applied {
		if acknowledged {
			return nil, zero, errors.New("unsupported ACK without original effect")
		}
		return expected, zero, nil
	}
	receipt := original.Effects["enrichment_command_receipts"]
	if len(receipt.Rows) != 1 {
		return nil, zero, errors.New("original receipt journal missing")
	}
	digest := receipt.Rows[0]["result_digest"]
	if digest.Kind != "blob" || len(digest.Blob) != 32 || bytes.Equal(digest.Blob, make([]byte, 32)) {
		return nil, zero, errors.New("original effective result digest invalid")
	}
	h := c.request.header
	image := expected["catalog_credit_images"]
	image.Rows = []map[string]effectiveRawCell{effectiveModelRow(map[string]any{
		"id": 1, "entity_id": h.entityID, "revision": 1, "entity_incarnation": h.entityIncarnation[:],
		"source_revision": h.sourceRevision, "identity_revision": h.identityRevision, "metadata_revision": h.metadataRevision, "owner_revision": h.ownerRevision,
		"policy_incarnation": h.policyIncarnation[:], "policy_revision": h.policyRevision, "consent_revision": h.consentRevision, "metadata_language": h.language,
		"source_manifest_digest": h.sourceManifest[:], "build_request_digest": c.digest[:], "manifest_algorithm": "portico-credit-manifest-sha256-chain-v1",
		"build_expected_count": 0, "build_expected_bytes": 0, "build_expected_digest": h.expectedManifest[:],
		"build_staged_count": 0, "build_staged_bytes": 0, "build_verified_count": 0, "build_verified_bytes": 0,
		"build_seed": h.expectedManifest[:], "build_chain": h.expectedManifest[:], "build_after_ord": -1,
		"member_count": 0, "prepared_bytes": 0, "manifest_digest": nil, "current_input_digest": h.declaredInput[:], "sealed": 0,
		"remaining_rows": 0, "remaining_bytes": 0, "reclaim_state": "held", "reclaim_after_ord": -1,
		"command_incarnation": original.Retirement[:], "last_command_sequence": 0, "acknowledged_command_sequence": 0,
		"job_revision": 1, "job_lease_owner": 0, "job_lease_generation": 0, "job_lease_expires_ms": 0, "job_attempts": 0, "job_next_attempt_ms": nil, "job_due_ms": nil,
		"source_proof_owner": nil, "source_proof_incarnation": nil, "source_proof_sequence": nil,
		"source_proof_kind": nil, "source_proof_publication_id": nil, "source_proof_provider": nil,
	})}
	wantReceipt := effectiveModelRow(map[string]any{
		"provider_operation_id": c.request.ownerID, "command_incarnation": c.identity.Incarnation[:], "command_sequence": c.identity.Sequence,
		"operation_id": c.identity.OperationID[:], "request_digest": c.digest[:], "command_kind": "effective_stage",
		"accepted_revision": c.request.entity.revision, "applied_ms": crashClock.UnixMilli(), "entity_id": c.request.entity.id,
		"after_item": c.request.owner.afterItem, "after_ordinal": 0, "publication_id": nil, "effective_image_id": 1, "effective_image_revision": 1,
		"result_count": 0, "result_digest": digest.Blob,
		"seal_version": nil, "seal_evidence_id": nil, "seal_subject_id": nil, "seal_parser_version": nil,
		"seal_provider": nil, "seal_subject_kind": nil, "seal_provider_id": nil,
		"seal_language": nil, "seal_region": nil, "seal_request_shape": nil, "seal_observation_digest": nil,
		"seal_owner_generation": nil, "seal_build_id": nil, "seal_build_generation": nil, "seal_content_revision": nil,
		"seal_algorithm": nil, "seal_commitment": nil, "seal_verified_rows": nil, "seal_canonical_bytes": nil, "seal_retained_bytes": nil,
		"proof_revision": 1, "proof_pin_count": 0, "proof_reclaim_due_ms": nil, "proof_cleanup_after_sequence": 0,
		"upstream_evidence_owner": nil, "upstream_evidence_incarnation": nil, "upstream_evidence_sequence": nil, "upstream_evidence_kind": nil,
		"source_dependency_owner": nil, "source_dependency_incarnation": nil, "source_dependency_sequence": nil, "source_dependency_kind": nil,
		"source_dependency_publication_id": nil, "source_dependency_provider": nil,
	})
	receipt.Rows = []map[string]effectiveRawCell{wantReceipt}
	if err := compareEffectiveJournal(effectiveRawJournal{"catalog_credit_images": image, "enrichment_command_receipts": receipt}, original.Effects); err != nil {
		return nil, zero, err
	}
	expected["catalog_credit_images"], expected["enrichment_command_receipts"] = image, receipt
	owner.Rows[0]["credit_image_id"], _ = effectiveCell(int64(1))
	owner.Rows[0]["last_command_sequence"], _ = effectiveCell(c.identity.Sequence)
	if acknowledged {
		owner.Rows[0]["acknowledged_command_sequence"], _ = effectiveCell(c.identity.Sequence)
	}
	expected["metadata_provider_operations"] = owner
	result := EffectiveStageBeginResult{ProviderOperationID: c.request.ownerID, EntityID: c.request.entity.id, ObservedCatalogRevision: c.request.entity.revision,
		AppliedMS: crashClock.UnixMilli(), AfterItem: c.request.owner.afterItem, ImageID: 1, ImageRevision: 1}
	copy(result.Digest[:], digest.Blob)
	return expected, result, nil
}

type effectiveStepConn struct {
	writerDriverConn
	native   writerDriverConn
	original *effectiveCrashOriginal
}

func (c *effectiveStepConn) ExecContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Result, error) {
	if q == "COMMIT" && c.original.Effects == nil {
		journal, err := readEffectiveNativeEffects(ctx, c.native)
		if err != nil {
			return nil, err
		}
		c.original.Effects = journal
	}
	result, err := c.writerDriverConn.ExecContext(ctx, q, args)
	return result, err
}

func effectiveStepIDs(operation string) []stmt.ID {
	ids := []stmt.ID{effectiveDisclosureLibrary, effectiveDisclosureSubject, enrichmentEffectiveOwner, enrichmentEffectiveOwner, enrichmentEffectiveReceipt}
	switch operation {
	case "stage":
		return append(ids, enrichmentEffectiveOwner, enrichmentEffectiveReceipt, enrichmentEffectiveEntity,
			enrichmentEffectiveLibrary, enrichmentEffectiveLanguage, enrichmentEffectivePolicy, enrichmentEffectiveProvider, enrichmentEffectiveConsent,
			enrichmentEffectiveSelection, enrichmentEffectiveEvidence, enrichmentEffectiveSubject, enrichmentEffectivePublication, enrichmentEffectiveCreditHead,
			enrichmentEffectiveCapture, enrichmentEffectiveAssociation, enrichmentEffectiveAsset, enrichmentEffectiveObject, enrichmentEffectiveSource,
			enrichmentEffectiveInsert, enrichmentEffectiveAttach, enrichmentEffectiveRecord)
	case "stage_replay", "resolve":
		return ids
	case "ack":
		return append(ids, effectiveReceiptAck)
	default:
		return nil
	}
}

func effectiveStepPhases(operation string) []string {
	var phases []string
	for i, id := range effectiveStepIDs(operation) {
		phases = append(phases, fmt.Sprintf("native_%02d_%s", i, id))
	}
	return phases
}

func effectiveStepDecision(c crashCase) (applied, acknowledged bool, err error) {
	valid := c.Domain == "effective_steps" && len(effectiveStepIDs(c.Operation)) != 0
	if c.CommitFault != "" {
		valid = valid && c.Boundary == "uncertain_return" && (c.CommitFault == "before" || c.CommitFault == "after")
	} else {
		boundaries := append([]string{"before_begin", "after_begin", "before_commit", "after_commit", "physical_return", "api_return"}, effectiveStepPhases(c.Operation)...)
		found := false
		for _, boundary := range boundaries {
			found = found || boundary == c.Boundary
		}
		valid = valid && found
	}
	if !valid {
		return false, false, fmt.Errorf("unsupported effective step journal: %+v", c)
	}
	return c.Operation != "stage" || crashCommitted(c), c.Operation == "ack" && crashCommitted(c), nil
}

func checkEffectiveStepReturn(t *testing.T, f *effectiveFirstLightFixture, original *effectiveCrashOriginal, operation string, result EffectiveStageResult, ack AckResult) {
	t.Helper()
	want, saved, err := effectiveStepExpectation(f.command, original, true, operation == "ack")
	if err != nil {
		t.Fatal(err)
	}
	got, err := readEffectiveJournal(t.Context(), f.executor.db, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	if err := compareEffectiveJournal(want, got); err != nil {
		t.Fatal(err)
	}
	evidence := result.Outcome.Evidence
	if operation == "ack" {
		evidence = ack.Evidence
	}
	if evidence == nil || evidence.Commit != CommitSucceeded || evidence.PhysicalReturnedAt == nil || evidence.Purpose != EffectiveStage || evidence.Durability != Normal || evidence.Policy == nil ||
		f.authority.Usage() != ([4]governor.Amounts{}) || f.executor.db.Stats().InUse != 0 {
		t.Fatal("missing actual frame/custody evidence", evidence)
	}
	if operation == "ack" {
		ack.Evidence = nil
		if ack != (AckResult{AcknowledgedThrough: 1}) || !reflect.DeepEqual(result, EffectiveStageResult{}) {
			t.Fatal(result, ack)
		}
	} else {
		result.Outcome.Evidence = nil
		wantResult := EffectiveStageResult{Outcome: ReceiptOutcome{State: Applied, Reference: effectiveReference(f.command).Receipt, Replayed: operation != "stage"}, Value: saved}
		if result != wantResult || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal("effective complete return changed", result, wantResult, ack)
		}
	}
}

func crashEffectiveStepsFixture(t *testing.T, witness *crashWitness) (*effectiveFirstLightFixture, []string) {
	t.Helper()
	if len(effectiveStepIDs(witness.Case.Operation)) == 0 {
		t.Fatal("unsupported effective operation", witness.Case)
	}
	f := newEffectiveFirstLightFixture(t, func() time.Time { return crashClock })
	initial, err := readEffectiveJournal(t.Context(), f.executor.db, effectiveJournalTables)
	if err != nil {
		t.Fatal(err)
	}
	witness.Effective = &effectiveCrashOriginal{Initial: initial, Retirement: f.command.retirementIncarnation}
	witness.Path, witness.Reference = f.path, effectiveReference(f.command).Receipt
	if witness.Case.Operation != "stage" {
		result, err := f.writer.StageEffective(t.Context(), f.disclosure, f.command)
		if err != nil {
			t.Fatal(err)
		}
		witness.Effective.Effects, err = readEffectiveJournal(t.Context(), f.executor.db, []string{"catalog_credit_images", "enrichment_command_receipts"})
		if err != nil {
			t.Fatal(err)
		}
		checkEffectiveStepReturn(t, f, witness.Effective, "stage", result, AckResult{})
	}
	defs := append(enrichmentEffectiveBeginDefinitions(), enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, effectiveDispatchDefinitions()...)
	bySQL := map[string]stmt.ID{}
	for _, d := range defs {
		bySQL[strings.TrimSpace(d.SQL)] = d.ID
	}
	var phases []string
	observe := func(q string) {
		if id, ok := bySQL[strings.TrimSpace(q)]; ok {
			phase := fmt.Sprintf("native_%02d_%s", len(phases), id)
			phases = append(phases, phase)
			crashPoint(t, witness, phase)
		}
	}
	if err := f.executor.db.Close(); err != nil {
		t.Fatal(err)
	}
	connector, err := sqliteconn.Connector(f.path, f.executor.config)
	if err != nil {
		t.Fatal(err)
	}
	var committed atomic.Bool
	f.executor.openPool(connector, func(conn driver.Conn) driver.Conn {
		native := conn.(writerDriverConn)
		wrapped := &crashConn{writerDriverConn: native, t: t, witness: witness, commit: &committed, afterSQL: observe}
		return &effectiveStepConn{writerDriverConn: wrapped, native: native, original: witness.Effective}
	})
	f.executor.now = func() time.Time {
		if committed.Load() && f.executor.db.Stats().InUse == 0 {
			crashPoint(t, witness, "physical_return")
		}
		return crashClock
	}
	primeEffectiveFixturePool(t, f.executor)
	var result EffectiveStageResult
	var ack AckResult
	switch witness.Case.Operation {
	case "stage", "stage_replay":
		result, err = f.writer.StageEffective(t.Context(), f.disclosure, f.command)
	case "resolve":
		result, err = f.writer.ResolveEffective(t.Context(), f.disclosure, effectiveReference(f.command))
	case "ack":
		ack, err = f.writer.AckEffective(t.Context(), f.disclosure, effectiveReference(f.command))
	}
	if witness.Case.CommitFault != "" {
		if !errors.Is(err, ErrResolveRequired) || !errors.Is(err, errCrashCommitReply) || !reflect.DeepEqual(result, EffectiveStageResult{}) || !reflect.DeepEqual(ack, AckResult{}) || !f.executor.ambiguous || !f.writer.effective.fenced {
			t.Fatal("effective unknown COMMIT escaped quarantine", result, ack, err)
		}
		witness.Unknown = true
		crashPoint(t, witness, "uncertain_return")
	} else {
		if err != nil {
			t.Fatal(err)
		}
		checkEffectiveStepReturn(t, f, witness.Effective, witness.Case.Operation, result, ack)
	}
	if !reflect.DeepEqual(phases, effectiveStepPhases(witness.Case.Operation)) {
		t.Fatal("effective native phase inventory changed", phases)
	}
	crashPoint(t, witness, "api_return")
	if witness.Case.Boundary != "trace" {
		t.Fatal("effective requested boundary not reached", witness.Case)
	}
	return f, phases
}

func verifyEffectiveStepsCrash(t *testing.T, command *effectiveStageBeginCommand, witness crashWitness) {
	t.Helper()
	if witness.Reference != effectiveReference(command).Receipt || witness.Unknown != (witness.Case.CommitFault != "") {
		t.Fatal("effective original identity changed", witness)
	}
	applied, acknowledged, err := effectiveStepDecision(witness.Case)
	if err != nil {
		t.Fatal(err)
	}
	want, saved, err := effectiveStepExpectation(command, witness.Effective, applied, acknowledged)
	if err != nil {
		t.Fatal(err)
	}
	e := reopenCrashExecutor(t, witness.Path)
	check := func(expected effectiveRawJournal) {
		t.Helper()
		got, err := readEffectiveJournal(t.Context(), e.db, effectiveJournalTables)
		if err != nil {
			t.Fatal(err)
		}
		if err := compareEffectiveJournal(expected, got); err != nil {
			t.Fatal(err)
		}
	}
	check(want)
	w, cap, _, _, authority := bindEffectiveFixtureRuntime(t, e, command, e.config)
	if cap.scope.fixture != nil {
		t.Fatal("restart gained fresh source permission")
	}
	request := effectiveReference(command)
	for range 2 {
		result, err := w.ResolveEffective(t.Context(), cap, request)
		if err != nil {
			t.Fatal(err)
		}
		result.Outcome.Evidence = nil
		expected := EffectiveStageResult{Outcome: ReceiptOutcome{State: Pending, Reference: request.Receipt}}
		if applied && !acknowledged {
			expected.Outcome.State, expected.Outcome.Replayed, expected.Value = Applied, true, saved
		}
		if acknowledged {
			expected.Outcome.State = ResultRetired
		}
		if result != expected {
			t.Fatal("effective restart projection changed", result, expected)
		}
	}
	if !applied {
		out, err := w.StageEffective(t.Context(), cap, command)
		var refusal *Refusal
		if !errors.As(err, &refusal) || refusal.Code != RefusalUnqualified || !reflect.DeepEqual(out, EffectiveStageResult{}) {
			t.Fatal(out, err)
		}
		if ack, err := w.AckEffective(t.Context(), cap, request); !errors.Is(err, ErrResolveRequired) || !reflect.DeepEqual(ack, AckResult{}) {
			t.Fatal(ack, err)
		}
		check(want)
	} else {
		for range 2 {
			ack, err := w.AckEffective(t.Context(), cap, request)
			if err != nil || ack.AcknowledgedThrough != 1 {
				t.Fatal(ack, err)
			}
		}
		retired, _, err := effectiveStepExpectation(command, witness.Effective, true, true)
		if err != nil {
			t.Fatal(err)
		}
		check(retired) // Complete retained receipt/image/source bytes, even below the floor.
		out, err := w.ResolveEffective(t.Context(), cap, request)
		if err != nil {
			t.Fatal(err)
		}
		out.Outcome.Evidence = nil
		if out != (EffectiveStageResult{Outcome: ReceiptOutcome{State: ResultRetired, Reference: request.Receipt}}) {
			t.Fatal("retired effective payload is not zero", out)
		}
	}
	if authority.Usage() != ([4]governor.Amounts{}) || e.db.Stats().InUse != 0 {
		t.Fatal("effective recovery stranded custody")
	}
}

func TestCrashEffectiveStepsRejectRawCorruption(t *testing.T) {
	witness := crashWitness{Case: crashCase{"effective_steps", "stage", "trace", ""}}
	f, _ := crashEffectiveStepsFixture(t, &witness)
	command := f.command
	if joined, err := closeEffectiveFirstLightFixture(f); err != nil || !joined {
		t.Fatal(joined, err)
	}
	e := reopenCrashExecutor(t, witness.Path)
	w, cap, _, _, _ := bindEffectiveFixtureRuntime(t, e, command, e.config)
	if cap.scope.fixture != nil {
		t.Fatal("corruption control gained source permission")
	}
	_, saved, err := effectiveStepExpectation(command, witness.Effective, true, false)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := w.ResolveEffective(t.Context(), cap, effectiveReference(command))
	if err != nil || resolved.Outcome.Evidence == nil {
		t.Fatal(resolved, err)
	}
	resolved.Outcome.Evidence = nil
	if resolved != (EffectiveStageResult{Outcome: ReceiptOutcome{State: Applied, Reference: witness.Reference, Replayed: true}, Value: saved}) {
		t.Fatal("control reopen projection changed", resolved)
	}
	for _, retired := range []bool{false, true} {
		if retired {
			ack, err := w.AckEffective(t.Context(), cap, effectiveReference(command))
			if err != nil || ack.AcknowledgedThrough != 1 {
				t.Fatal(ack, err)
			}
		}
		want, _, err := effectiveStepExpectation(command, witness.Effective, true, retired)
		if err != nil {
			t.Fatal(err)
		}
		conn, err := e.db.Conn(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			policy := e.config
			policy.Synchronous = sqliteconn.Normal
			if _, err := sqliteconn.Assert(t.Context(), conn, policy); err != nil {
				t.Fatal(err)
			}
			baseline, err := readEffectiveJournal(t.Context(), conn, effectiveJournalTables)
			if err != nil {
				t.Fatal(err)
			}
			if err := compareEffectiveJournal(want, baseline); err != nil {
				t.Fatal(err)
			}
			for _, mutation := range []struct{ name, sql string }{
				{"receipt_identity", `UPDATE enrichment_command_receipts SET operation_id=zeroblob(16)`},
				{"receipt_request", `UPDATE enrichment_command_receipts SET request_digest=zeroblob(32)`},
				{"receipt_digest", `UPDATE enrichment_command_receipts SET result_digest=zeroblob(32)`},
				{"receipt_cursor", `UPDATE enrichment_command_receipts SET after_item='WRONG-SAVED-CURSOR'`},
				{"receipt_revision", `UPDATE enrichment_command_receipts SET accepted_revision=accepted_revision+1`},
				{"receipt_variant", `UPDATE enrichment_command_receipts SET command_kind='credit_stage'`},
				{"receipt_carrier", `UPDATE enrichment_command_receipts SET publication_id='WRONG-CARRIER'`},
				{"receipt_count", `UPDATE enrichment_command_receipts SET result_count=1`},
				{"image_nonce", `UPDATE catalog_credit_images SET command_incarnation=zeroblob(16)`},
				{"image_revision", `UPDATE catalog_credit_images SET revision=revision+1`},
				{"image_request", `UPDATE catalog_credit_images SET build_request_digest=zeroblob(32)`},
				{"image_chain", `UPDATE catalog_credit_images SET build_chain=zeroblob(32)`},
				{"image_cursor", `UPDATE catalog_credit_images SET build_after_ord=0`},
				{"image_reclaim", `UPDATE catalog_credit_images SET reclaim_state='ready'`},
				{"owner_unprojected", `UPDATE metadata_provider_operations SET query_title='WRONG-QUERY'`},
				{"owner_result", `UPDATE metadata_provider_operations SET result_digest='WRONG-OWNER-RESULT'`},
				{"serving_head", `UPDATE catalog_entities SET credit_image_id=1`},
				{"source_revision", `UPDATE inventory_objects SET revision='WRONG-SOURCE-REVISION'`},
			} {
				t.Run(fmt.Sprintf("retired_%t/%s", retired, mutation.name), func(t *testing.T) {
					if _, err := conn.ExecContext(t.Context(), "SAVEPOINT corrupt_effective"); err != nil {
						t.Fatal(err)
					}
					defer func() {
						if _, err := conn.ExecContext(t.Context(), "ROLLBACK TO corrupt_effective"); err != nil {
							t.Error(err)
						}
						if _, err := conn.ExecContext(t.Context(), "RELEASE corrupt_effective"); err != nil {
							t.Error(err)
						}
					}()
					if _, err := conn.ExecContext(t.Context(), mutation.sql); err != nil {
						t.Fatal("mutation failed before oracle", err)
					}
					got, err := readEffectiveJournal(t.Context(), conn, effectiveJournalTables)
					if err != nil {
						t.Fatal(err)
					}
					if err := compareEffectiveJournal(want, got); err == nil {
						t.Fatal("effective oracle accepted native corruption")
					}
				})
			}
		}()
	}
	for _, unsupported := range []crashCase{
		{"effective_steps", "future_accept", "api_return", ""}, {"effective_steps", "stage", "inside_fsync", ""},
		{"effective_steps", "stage", "after_commit", "before"}, {"effective_steps", "stage", "uncertain_return", "guess"},
	} {
		if _, _, err := effectiveStepDecision(unsupported); err == nil {
			t.Fatal("unsupported journal became a passing expectation", unsupported)
		}
	}
}

func TestCrashReplayEffectiveNativeSteps(t *testing.T) {
	t.Helper()
	if raceDetector {
		// This sequential child matrix exceeds one shard's race budget. The
		// same cells run without -race; a skipped opt-in target is not evidence.
		t.Skip("crash replay is covered without -race; race child matrix exceeds the shard budget")
	}

	for _, operation := range []string{"stage", "stage_replay", "resolve", "ack"} {
		t.Run(operation, func(t *testing.T) {
			witness := crashWitness{Case: crashCase{"effective_steps", operation, "trace", ""}}
			f, phases := crashEffectiveStepsFixture(t, &witness)
			command := f.command
			if joined, err := closeEffectiveFirstLightFixture(f); err != nil || !joined {
				t.Fatal("effective trace did not join", joined, err)
			}
			boundaries := append([]string{"before_begin", "after_begin"}, phases...)
			boundaries = append(boundaries, "before_commit", "after_commit", "physical_return", "api_return")
			for _, boundary := range boundaries {
				t.Run(boundary, func(t *testing.T) {
					got := runCrashChild(t, crashCase{"effective_steps", operation, boundary, ""})
					verifyEffectiveStepsCrash(t, command, got)
				})
			}
			for _, fault := range []string{"before", "after"} {
				t.Run("uncertain_return/"+fault, func(t *testing.T) {
					got := runCrashChild(t, crashCase{"effective_steps", operation, "uncertain_return", fault})
					verifyEffectiveStepsCrash(t, command, got)
				})
			}
		})
	}
}
