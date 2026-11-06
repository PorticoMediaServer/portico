package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	enrichmentSourceAdvance      stmt.ID = "enrichment.credit_stage.source_advance"
	enrichmentSourceMemberInsert stmt.ID = "enrichment.credit_stage.insert_source_member"
	sourceStageChunkMembers              = 16 // write quantum, never a history/proof quota
)

// Draft009 native names follow the adopted operation-owned source direction.
// W adds this finite definition to its ONE composition when the complete
// command/receipt/authority/resource recipe is ready; this is not registration.
const sourceStagePrefixRead stmt.ID = "enrichment.credit_stage.source_prefix"

func enrichmentSourceStageDefinitions() []stmt.Definition {
	incarnation := make([]byte, 16)
	for i := range incarnation {
		incarnation[i] = byte(16 + i)
	}
	defs := []stmt.Definition{{
		ID: enrichmentSourceAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_stage.go:advanceSourceStagePrefix"},
		SQL: `UPDATE metadata_provider_operations SET source_next_position=?,source_content_revision=source_content_revision+1
 WHERE id=? AND command_incarnation=? AND generation=? AND source_owner_generation=?
 AND source_publication_id=? AND source_next_position=? AND source_expected_members=? AND source_content_revision=?
 AND last_command_sequence=? AND acknowledged_command_sequence=? AND entity_id=? AND provider=?
 AND phase=? AND status=? AND lease=? AND lease_until=?
 RETURNING source_next_position,source_content_revision`,
		Params: []stmt.Param{
			{Name: "next_position", Type: stmt.Integer}, {Name: "operation_id", Type: stmt.Text},
			{Name: "incarnation", Type: stmt.Blob}, {Name: "generation", Type: stmt.Integer},
			{Name: "source_owner_generation", Type: stmt.Integer}, {Name: "publication_id", Type: stmt.Text},
			{Name: "expected_next", Type: stmt.Integer}, {Name: "expected_members", Type: stmt.Integer},
			{Name: "expected_revision", Type: stmt.Integer}, {Name: "last_sequence", Type: stmt.Integer},
			{Name: "ack_sequence", Type: stmt.Integer}, {Name: "entity_id", Type: stmt.Integer},
			{Name: "provider", Type: stmt.Text}, {Name: "phase", Type: stmt.Text},
			{Name: "status", Type: stmt.Text}, {Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text},
		},
		Cohorts: []stmt.Cohort{{Name: "exact_source_prefix", Bindings: []any{
			int64(3), "operation-one", incarnation, int64(31), int64(31),
			"00112233445566778899aabbccddeeff", int64(2), int64(5), int64(47),
			int64(11), int64(11), int64(101), "fixture", "enrichment-v1", "staged", "native-lease", "2100-11-05T12:00:00Z",
		}}},
		Plan: stmt.Plan{Hot: true},
	}, {
		ID: enrichmentSourceMemberInsert, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_stage.go:stageSourceMembers"},
		SQL: `INSERT INTO credit_source_publication_members(publication_id,entity_id,provider,position,source_row_id,reference_status)
 SELECT ?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM metadata_provider_operations o
 JOIN credit_source_publications p ON p.publication_id=o.source_publication_id AND p.entity_id=o.entity_id AND p.provider=o.provider
 WHERE o.id=? AND o.command_incarnation=? AND o.generation=? AND o.source_owner_generation=?
 AND o.source_publication_id=? AND o.source_next_position=? AND o.source_expected_members=? AND o.source_content_revision=?
 AND o.last_command_sequence=? AND o.acknowledged_command_sequence=? AND o.entity_id=? AND o.provider=?
 AND o.phase=? AND o.status=? AND o.lease=? AND o.lease_until=?
 AND p.member_count=o.source_expected_members AND p.sealed=0)`,
		Params: []stmt.Param{
			{Name: "publication_id", Type: stmt.Text}, {Name: "entity_id", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text},
			{Name: "position", Type: stmt.Integer}, {Name: "source_row_id", Type: stmt.Text}, {Name: "reference_status", Type: stmt.Text},
			{Name: "operation_id", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "generation", Type: stmt.Integer},
			{Name: "owner_generation", Type: stmt.Integer}, {Name: "expected_publication", Type: stmt.Text},
			{Name: "expected_next", Type: stmt.Integer}, {Name: "expected_members", Type: stmt.Integer}, {Name: "expected_revision", Type: stmt.Integer},
			{Name: "last_sequence", Type: stmt.Integer}, {Name: "ack_sequence", Type: stmt.Integer},
			{Name: "expected_entity", Type: stmt.Integer}, {Name: "expected_provider", Type: stmt.Text},
			{Name: "phase", Type: stmt.Text}, {Name: "status", Type: stmt.Text}, {Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text},
		},
		Cohorts: []stmt.Cohort{{Name: "exact_source_member", Bindings: []any{
			"00112233445566778899aabbccddeeff", int64(101), "fixture", int64(2), "00000000000000000000000000000003", "new",
			"operation-one", incarnation, int64(31), int64(31), "00112233445566778899aabbccddeeff", int64(2), int64(5), int64(47),
			int64(11), int64(11), int64(101), "fixture", "enrichment-v1", "staged", "native-lease", "2100-11-05T12:00:00Z",
		}}},
	}}
	// The same fixed owner/publication predicate is read before VALUES writes;
	// final prefix CAS checks it again in the same single-writer transaction.
	read := defs[0]
	read.ID = sourceStagePrefixRead
	read.Kind = stmt.Read

	read.SQL = `SELECT o.source_next_position FROM metadata_provider_operations AS o INNER JOIN credit_source_publications AS p ON p.publication_id=o.source_publication_id AND p.entity_id=o.entity_id AND p.provider=o.provider
 WHERE o.id=? AND o.command_incarnation=? AND o.generation=? AND o.source_owner_generation=? AND o.source_publication_id=? AND o.source_next_position=? AND o.source_expected_members=? AND o.source_content_revision=? AND o.last_command_sequence=? AND o.acknowledged_command_sequence=? AND o.entity_id=? AND o.provider=? AND o.phase=? AND o.status=? AND o.lease=? AND o.lease_until=? AND p.member_count=o.source_expected_members AND p.sealed=0 AND p.seal_proof_owner IS NULL`

	read.Params = append([]stmt.Param(nil), defs[0].Params[1:]...)
	read.Cohorts = []stmt.Cohort{{Name: "exact_source_prefix", Bindings: defs[0].Cohorts[0].Bindings[1:]}}
	defs[1].SQL = `INSERT INTO credit_source_publication_members(publication_id,entity_id,provider,position,source_row_id,reference_status) VALUES(?,?,?,?,?,?)`
	defs[1].Params = defs[1].Params[:6]
	defs[1].Cohorts[0].Bindings = defs[1].Cohorts[0].Bindings[:6]
	return enrichmentStagePlanHints(append(defs, read))

}

type sourceStagePrefix struct {
	operationID, provider, publicationID, phase, status, lease, leaseUntil string
	incarnation                                                            [16]byte
	entityID, generation, ownerGeneration, revision, next, expected        int64
	lastSequence, acknowledgedSequence                                     int64
}

type sourceStageProgress struct{ next, revision int64 }

type sourceStageMember struct {
	position               int64
	sourceRowID, reference string
}

// stageSourceMembers writes one bounded prefix and its covering sourceR CAS in
// the SAME turn. Source rows are already immutable native facts; this never
// synthesizes a person identity/tuple hash or grants provenance. The caller
// still owns receipt/last/terminal integration and rollback on every error.
func stageSourceMembers(ctx context.Context, turn *commandTurn, insert, advance statementStep, owner sourceStagePrefix, identity CommandIdentity, members []sourceStageMember) (out sourceStageProgress, err error) {
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != CreditStage || insert.id != enrichmentSourceMemberInsert || advance.id != enrichmentSourceAdvance {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, turn.transaction.ctx, out, err) }()
	if len(members) == 0 || len(members) > sourceStageChunkMembers || !validSourceStagePrefix(owner, identity, int64(len(members))) {
		return out, ErrConflict
	}
	for i, member := range members {
		if member.position != owner.next+int64(i) || len(member.sourceRowID) != 32 || (member.reference != "new" && member.reference != "exact" && member.reference != "ambiguous") {
			return out, ErrInvalid
		}
		for _, ch := range member.sourceRowID {
			if (ch < '0' || ch > '9') && (ch < 'a' || ch > 'f') {
				return out, ErrInvalid
			}
		}
		for j := 0; j < i; j++ {
			if members[j].sourceRowID == member.sourceRowID {
				return out, ErrInvalid
			}
		}
	}
	step, err := sealStep(turn, sourceStagePrefixRead)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, sourcePrefixBindings(owner, identity)...)
	if err != nil {
		return out, err
	}
	var actualNext int64
	if err := row.Scan(&actualNext); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	if actualNext != owner.next {
		return out, ErrConflict
	}
	for _, member := range members {
		result, err := turn.exec(ctx, insert, owner.publicationID, owner.entityID, owner.provider, member.position, member.sourceRowID, member.reference)
		if err != nil {
			return out, err
		}
		n, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		if n != 1 {
			return out, ErrConflict
		}
	}

	return advanceSourceStagePrefix(ctx, turn, advance, owner, identity, int64(len(members)))
}

func validSourceStagePrefix(owner sourceStagePrefix, identity CommandIdentity, count int64) bool {
	return owner.operationID != "" && owner.provider != "" && owner.publicationID != "" && owner.entityID > 0 &&
		owner.generation > 0 && owner.ownerGeneration == owner.generation && owner.revision > 0 && owner.revision < math.MaxInt64 &&
		owner.next >= 0 && owner.expected >= owner.next && count > 0 && count <= owner.expected-owner.next &&
		owner.phase == effectiveBeginPhase && owner.status == "staged" && owner.lease != "" && owner.leaseUntil != "" &&
		owner.incarnation == identity.Incarnation && owner.lastSequence >= 0 && owner.lastSequence == owner.acknowledgedSequence &&
		identity.Sequence > owner.lastSequence && identity.Sequence-owner.lastSequence == 1
}

// A private effect step, NOT an accepting command or verified-source minter.
// The closed caller first resolves history and checks full current authority,
// input/upstream proof, owner/source/lease vector and complete W admission. It
// inserts exactly these members and the immutable result/sequence in THIS turn,
// or rolls everything back. No Applied, seal bit or receipt is synthesized here.
func advanceSourceStagePrefix(ctx context.Context, turn *commandTurn, step statementStep, owner sourceStagePrefix, identity CommandIdentity, count int64) (out sourceStageProgress, err error) {
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != CreditStage || step.id != enrichmentSourceAdvance {
		return sourceStageProgress{}, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, turn.transaction.ctx, out, err) }()
	if !validSourceStagePrefix(owner, identity, count) {
		return sourceStageProgress{}, ErrConflict
	}
	check, err := sealStep(turn, sourceStagePrefixRead)
	if err != nil {
		return sourceStageProgress{}, err
	}
	current, err := turn.queryRow(ctx, check, sourcePrefixBindings(owner, identity)...)
	if err != nil {
		return sourceStageProgress{}, err
	}
	var next int64
	if err := current.Scan(&next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sourceStageProgress{}, ErrConflict
		}
		return sourceStageProgress{}, err
	}
	if next != owner.next {
		return sourceStageProgress{}, ErrConflict
	}

	row, err := turn.queryRow(ctx, step, append([]any{owner.next + count}, sourcePrefixBindings(owner, identity)...)...)
	if err != nil {
		return sourceStageProgress{}, err
	}
	var result sourceStageProgress
	if err := row.Scan(&result.next, &result.revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sourceStageProgress{}, ErrConflict
		}
		return sourceStageProgress{}, err
	}
	if result.next != owner.next+count || result.revision != owner.revision+1 {
		return sourceStageProgress{}, invalid("source_stage_returned_prefix")
	}
	return result, nil
}

func sourcePrefixBindings(owner sourceStagePrefix, identity CommandIdentity) []any {
	return []any{owner.operationID, owner.incarnation[:], owner.generation, owner.ownerGeneration, owner.publicationID, owner.next, owner.expected, owner.revision, owner.lastSequence, owner.acknowledgedSequence, owner.entityID, owner.provider, owner.phase, owner.status, owner.lease, owner.leaseUntil}
}
