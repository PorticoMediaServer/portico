package writer

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/stmttest"
)

// This is a minimal COMPONENT overlay for the directed009 source frontier,
// not INT's full candidate, a qualified profile or actual source-seal proof.
func draftSourceStageFixture(t *testing.T) (*executor, sourceStagePrefix, CommandIdentity) {
	t.Helper()
	w, _, _ := testExecutor(t)
	base, _, _ := effectiveBeginNativePopulation(t, w)
	for _, q := range []string{
		`ALTER TABLE metadata_provider_operations ADD COLUMN source_publication_id TEXT REFERENCES credit_source_publications(publication_id)`,
		`ALTER TABLE metadata_provider_operations ADD COLUMN source_next_position INTEGER`,
		`ALTER TABLE metadata_provider_operations ADD COLUMN source_expected_members INTEGER`,
		`ALTER TABLE metadata_provider_operations ADD COLUMN source_content_revision INTEGER NOT NULL DEFAULT 1 CHECK(source_content_revision>0)`,
		`ALTER TABLE metadata_provider_operations ADD COLUMN source_owner_generation INTEGER`,
		`CREATE UNIQUE INDEX draft009_source_publication_owner ON metadata_provider_operations(source_publication_id)`,
		`INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed)
 VALUES('00112233445566778899aabbccddeeff',101,'fixture','opaque-source:241','2026-11-06T10:00:00+00:00',5,0)`,
		`UPDATE metadata_provider_operations SET phase='enrichment-v1',acknowledged_command_sequence=11,
 lease_until='2100-11-05T12:00:00Z',source_publication_id='00112233445566778899aabbccddeeff',
 source_next_position=2,source_expected_members=5,source_content_revision=47,source_owner_generation=31 WHERE id='operation-one'`,
	} {
		if _, err := w.db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}
	// Pre-existing immutable source facts stand in for the separate typed people
	// and source-row driver. They do not stand in for its eventual proof.
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("%032x", i+1)
		if _, err := w.db.ExecContext(t.Context(), `INSERT INTO credit_source_rows(
 source_row_id,entity_id,provider,tuple_digest,original_record_reference,original_credit_id,
 original_provider_person_id,original_person_identity_key,original_person_token,original_target_kind,
 original_target_id,original_credited_name,original_role,original_department,original_credit_class,original_source_ordinal)
 VALUES(?,101,'fixture',?,'opaque-source:241',?,'201','fixture-person','person-token','person','person-token','Credited name','Pilot','','cast',?)`, id, strings.Repeat("a", 64), fmt.Sprint("credit:", i), int64(7+i)); err != nil {
			t.Fatal(err)
		}
		if i < 2 {
			if _, err := w.db.ExecContext(t.Context(), `INSERT INTO credit_source_publication_members(publication_id,entity_id,provider,position,source_row_id,reference_status)
 VALUES('00112233445566778899aabbccddeeff',101,'fixture',?,?,'new')`, int64(i), id); err != nil {
				t.Fatal(err)
			}
		}
	}
	owner := sourceStagePrefix{
		operationID: "operation-one", provider: "fixture", publicationID: "00112233445566778899aabbccddeeff",
		phase: effectiveBeginPhase, status: "staged", lease: "native-lease", leaseUntil: "2100-11-05T12:00:00Z",
		incarnation: base.incarnation, entityID: 101, generation: 31, ownerGeneration: 31, revision: 47,
		next: 2, expected: 5, lastSequence: 11, acknowledgedSequence: 11,
	}
	return w, owner, CommandIdentity{Incarnation: base.incarnation, Sequence: 12, OperationID: [16]byte{251}}
}

func draftSourceStageTurn(t *testing.T, w *executor) (*commandTurn, statementStep) {
	t.Helper()
	// Virtual, deliberately unqualified resource estimates for this primitive.
	recipe := turnRecipe{limit: Work{4096, 1000000, 4096, 32 << 20, 25 * time.Millisecond}, setup: Work{16, 500, 16, 65536, time.Millisecond}, terminal: Work{16, 500, 16, 65536, time.Millisecond}}
	tx, err := w.begin(t.Context(), CreditStage, recipe, &testCustody{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = tx.rollback(nil) })
	registry, err := stmttest.New(t, enrichmentSourceStageDefinitions())
	if err != nil {
		t.Fatal(err)
	}
	step := statementStep{enrichmentSourceAdvance, Work{16, 10000, 64, 65536, time.Millisecond}}
	insert := statementStep{enrichmentSourceMemberInsert, Work{16, 10000, 64, 65536, time.Millisecond}}
	turn, err := newCommandTurn(tx, registry, []statementStep{step, insert})
	if err != nil {
		t.Fatal(err)
	}
	return turn, step
}

func TestDraftSourceStagePrefixNativeCASAndRollback(t *testing.T) {
	w, owner, identity := draftSourceStageFixture(t)
	turn, step := draftSourceStageTurn(t, w)
	insert := turn.steps[enrichmentSourceMemberInsert].statementStep
	got, err := stageSourceMembers(t.Context(), turn, insert, step, owner, identity, []sourceStageMember{
		{2, "00000000000000000000000000000003", "new"},
		{3, "00000000000000000000000000000004", "exact"},
	})
	if err != nil || got != (sourceStageProgress{4, 48}) {
		t.Fatal("actual prefix/revision RETURNING", got, err)
	}
	var next, revision, count, page, last, ack int64
	var after, phase, status string
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT source_next_position,source_content_revision,
 (SELECT member_count FROM credit_source_publications WHERE publication_id=source_publication_id),page,after_item,phase,status,
 last_command_sequence,acknowledged_command_sequence FROM metadata_provider_operations WHERE id='operation-one'`).Scan(&next, &revision, &count, &page, &after, &phase, &status, &last, &ack); err != nil {
		t.Fatal(err)
	}
	if next != 4 || revision != 48 || count != 5 || page != 59 || after != "opaque-page:61" || phase != effectiveBeginPhase || status != "staged" || last != 11 || ack != 11 {
		t.Fatal("prefix step changed another owner/counter", next, revision, count, page, after, phase, status, last, ack)
	}
	var memberCount int64
	if err := turn.transaction.conn.QueryRowContext(t.Context(), `SELECT count(*) FROM credit_source_publication_members WHERE publication_id=?`, owner.publicationID).Scan(&memberCount); err != nil || memberCount != 4 {
		t.Fatal("member and prefix did not advance together", memberCount, err)
	}
	if err := turn.closeRows(); err != nil {
		t.Fatal(err)
	}
	if _, err := turn.transaction.rollback(nil); err != nil {
		t.Fatal(err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT source_next_position,source_content_revision FROM metadata_provider_operations WHERE id='operation-one'`).Scan(&next, &revision); err != nil || next != 2 || revision != 47 {
		t.Fatal("rollback leaked prefix", next, revision, err)
	}
	if err := w.db.QueryRowContext(t.Context(), `SELECT count(*) FROM credit_source_publication_members WHERE publication_id=?`, owner.publicationID).Scan(&memberCount); err != nil || memberCount != 2 {
		t.Fatal("rollback leaked source member", memberCount, err)
	}
}

func TestDraftSourceStageMemberFailureRollsBackPrefix(t *testing.T) {
	w, owner, identity := draftSourceStageFixture(t)
	turn, step := draftSourceStageTurn(t, w)
	insert := turn.steps[enrichmentSourceMemberInsert].statementStep
	// The second source row does not exist. A caller must roll back the whole
	// command after a failed member, including the valid first INSERT.
	got, err := stageSourceMembers(t.Context(), turn, insert, step, owner, identity, []sourceStageMember{
		{2, "00000000000000000000000000000003", "new"},
		{3, "00000000000000000000000000000009", "new"},
	})
	if err == nil || got != (sourceStageProgress{}) {
		t.Fatal("missing source row accepted or leaked progress", got, err)
	}
	if _, err := turn.transaction.rollback(err); err == nil {
		t.Fatal("rollback lost original failure")
	}
	var next, revision, members int64
	if err := w.db.QueryRowContext(t.Context(), `SELECT source_next_position,source_content_revision,
 (SELECT count(*) FROM credit_source_publication_members WHERE publication_id=source_publication_id)
 FROM metadata_provider_operations WHERE id=?`, owner.operationID).Scan(&next, &revision, &members); err != nil || next != 2 || revision != 47 || members != 2 {
		t.Fatal("failed chunk survived rollback", next, revision, members, err)
	}
}

func TestDraftSourceStageRefusesStaleOrSealedNativeContext(t *testing.T) {
	for name, mutate := range map[string]string{
		"revision": `UPDATE metadata_provider_operations SET source_content_revision=48 WHERE id='operation-one'`,
		"scope":    `UPDATE credit_source_publications SET member_count=4 WHERE publication_id='00112233445566778899aabbccddeeff'`,
		"sealed":   `UPDATE credit_source_publications SET sealed=1 WHERE publication_id='00112233445566778899aabbccddeeff'`,
	} {
		t.Run(name, func(t *testing.T) {
			w, owner, identity := draftSourceStageFixture(t)
			if _, err := w.db.ExecContext(t.Context(), mutate); err != nil {
				t.Fatal(err)
			}
			turn, step := draftSourceStageTurn(t, w)
			got, err := advanceSourceStagePrefix(t.Context(), turn, step, owner, identity, 1)
			if !errors.Is(err, ErrConflict) || got != (sourceStageProgress{}) {
				t.Fatal("stale native source admitted", got, err)
			}
		})
	}
}

func TestDraftSourceStageRejectsOverflowEmptyAndWrongFrame(t *testing.T) {
	w, owner, identity := draftSourceStageFixture(t)
	turn, step := draftSourceStageTurn(t, w)
	for _, count := range []int64{0, -1, 4} {
		if got, err := advanceSourceStagePrefix(t.Context(), turn, step, owner, identity, count); !errors.Is(err, ErrConflict) || got != (sourceStageProgress{}) {
			t.Fatal("invalid prefix admitted", count, got, err)
		}
	}
	owner.revision = math.MaxInt64
	if _, err := advanceSourceStagePrefix(t.Context(), turn, step, owner, identity, 1); !errors.Is(err, ErrConflict) {
		t.Fatal("revision overflow admitted", err)
	}
	if got, err := advanceSourceStagePrefix(t.Context(), nil, step, owner, identity, 1); !errors.Is(err, ErrInvalid) || got != (sourceStageProgress{}) {
		t.Fatal("missing actual frame admitted", got, err)
	}
}
