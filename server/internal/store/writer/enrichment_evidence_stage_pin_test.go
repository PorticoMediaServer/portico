package writer

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

func evidenceBuildPinFixture(t *testing.T) (*executor, evidenceBuildPinInput, CommandIdentity) {
	t.Helper()
	w, _, _ := testExecutor(t)
	base, _, _ := effectiveBeginNativePopulation(t, w)
	for _, q := range []string{
		`UPDATE metadata_provider_operations SET phase='enrichment-v1',acknowledged_command_sequence=11,
 lease_until='2100-11-05T12:00:00Z' WHERE id='operation-one'`,
		`INSERT INTO metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape,incarnation,revision)
 VALUES(193,'fixture','movie','opaque:selection','en-CA','CA','detail-v1',zeroblob(16),199)`,
		`INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,sealed)
 VALUES('evidence-original:197',193,'fixture-parser','fixture','movie','opaque:selection','en-CA','CA','detail-v1',
 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','2026-11-06T12:00:00Z','fixture://component',0)`,
		`INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,
 state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision)
 SELECT 'build-original:211','evidence-original:197',47,id,command_incarnation,generation,
 'building','children',1,3,1,'declared-not-verified','component-not-manifest',1024,'2100-11-05T13:00:00Z',1
 FROM metadata_provider_operations WHERE id='operation-one'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	return w, evidenceBuildPinInput{
		operationID: "operation-one", buildID: "build-original:211", evidenceID: "evidence-original:197", provider: "fixture",
		lease: "native-lease", leaseUntil: "2100-11-05T12:00:00Z", entityID: 101,
		operationGeneration: 31, buildGeneration: 47, lastSequence: 11, acknowledgedSequence: 11,
	}, CommandIdentity{Incarnation: base.incarnation, Sequence: 12, OperationID: [16]byte{223}}
}

func evidenceBuildPinTurn(t *testing.T, w *executor) (*commandTurn, statementStep) {
	t.Helper()
	// Component-only virtual charges, not a production EvidenceStage recipe.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), EvidenceStage, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	registry, err := stmttest.New(t, append(enrichmentEvidencePinDefinitions(), enrichmentStageFactDefinitions()...))
	if err != nil {
		t.Fatal(err)
	}
	step := statementStep{enrichmentEvidencePinBuild, Work{16, 10000, 64, 65536, time.Millisecond}}
	turn, err := newCommandTurn(tx, registry, []statementStep{step, {stageBuildFacts, Work{16, 10000, 64, 65536, time.Millisecond}}})
	if err != nil {
		t.Fatal(err)
	}
	return turn, step
}

func TestEvidenceBuildPinUsesOriginal005IdentityAndRollsBack(t *testing.T) {
	w, input, identity := evidenceBuildPinFixture(t)
	turn, step := evidenceBuildPinTurn(t, w)
	got, err := pinEvidenceBuild(t.Context(), turn, step, input, identity)
	if err != nil || got != (evidenceBuildPinResult{input.buildID, 47, 31}) {
		t.Fatal("native build/owner generations aliased", got, err)
	}
	var pin string
	var page, last, ack int64
	var after string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT evidence_build_id,page,after_item,last_command_sequence,acknowledged_command_sequence
 FROM metadata_provider_operations WHERE id=?`, input.operationID).Scan(&pin, &page, &after, &last, &ack); err != nil || pin != input.buildID || page != 59 || after != "opaque-page:61" || last != 11 || ack != 11 {
		t.Fatal("pin changed progress or original identity", pin, page, after, last, ack, err)
	}
	if _, err := pinEvidenceBuild(t.Context(), turn, step, input, identity); !errors.Is(err, ErrConflict) {
		t.Fatal("fresh effect silently adopted an existing pin", err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	var original sql.NullString
	if err := w.db.QueryRowContext(t.Context(), `SELECT evidence_build_id FROM metadata_provider_operations WHERE id=?`, input.operationID).Scan(&original); err != nil || original.Valid {
		t.Fatal("rollback leaked build pin", original, err)
	}
}

func TestEvidenceBuildPinRefusesStale005Custody(t *testing.T) {
	for name, mutation := range map[string]string{
		"captured_owner_generation":    `UPDATE metadata_evidence_builds SET owner_generation=32 WHERE id='build-original:211'`,
		"independent_build_generation": `UPDATE metadata_evidence_builds SET generation=31 WHERE id='build-original:211'`,
		"already_sealed":               `UPDATE metadata_evidence SET sealed=1 WHERE id='evidence-original:197'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, input, identity := evidenceBuildPinFixture(t)
			if _, err := w.db.ExecContext(t.Context(), mutation); err != nil {
				t.Fatal(err)
			}
			turn, step := evidenceBuildPinTurn(t, w)
			got, err := pinEvidenceBuild(t.Context(), turn, step, input, identity)
			if !errors.Is(err, ErrConflict) || got != (evidenceBuildPinResult{}) {
				t.Fatal("stale build custody accepted", got, err)
			}
		})
	}
}
