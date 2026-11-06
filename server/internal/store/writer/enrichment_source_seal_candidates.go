package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	sealCandidateReasons      stmt.ID = "enrichment.candidate_retire.reasons"
	sealCandidateAliases      stmt.ID = "enrichment.candidate_retire.aliases"
	sealCandidateParent       stmt.ID = "enrichment.candidate_retire.parent"
	sealCandidateAdvance      stmt.ID = "enrichment.candidate_retire.advance"
	sealCandidateReady        stmt.ID = "enrichment.candidate_retire.ready"
	sealCandidateReasonRead   stmt.ID = "enrichment.candidate_retire.reason_keys"
	sealCandidateAliasRead    stmt.ID = "enrichment.candidate_retire.alias_keys"
	sealCandidateChildQuantum         = 16
)

func enrichmentCandidateRetirementDefinitions() []stmt.Definition {
	key := []stmt.Param{{Name: "candidate", Type: stmt.Text}, {Name: "ordinal", Type: stmt.Integer}}
	defs := []stmt.Definition{
		{ID: sealCandidateReasons, Kind: stmt.Write, SQL: `DELETE FROM metadata_candidate_reasons WHERE candidate_id=? AND ordinal=?`, Params: key},
		{ID: sealCandidateAliases, Kind: stmt.Write, SQL: `DELETE FROM metadata_candidate_aliases WHERE candidate_id=? AND ordinal=?`, Params: key},
		{ID: sealCandidateParent, Kind: stmt.Write, SQL: `DELETE FROM metadata_candidates WHERE id=? AND operation_id=? RETURNING evidence_id`, Params: []stmt.Param{{Name: "candidate", Type: stmt.Text}, {Name: "owner", Type: stmt.Text}}},
		{ID: sealCandidateAdvance, Kind: stmt.Write, SQL: `UPDATE metadata_provider_operations SET source_content_revision=source_content_revision+1 WHERE ` + sealOwnerPredicate + ` RETURNING source_content_revision`, Params: sealScopeParams()},
		{ID: sealCandidateReady, Kind: stmt.Read, SQL: `SELECT id,evidence_id FROM metadata_candidates INDEXED BY metadata_candidate_operation WHERE operation_id=? ORDER BY id LIMIT 1`, Params: []stmt.Param{{Name: "owner", Type: stmt.Text}}},
		{ID: sealCandidateReasonRead, Kind: stmt.Read, SQL: `SELECT ordinal FROM metadata_candidate_reasons WHERE candidate_id=? ORDER BY ordinal LIMIT 16`, Params: []stmt.Param{{Name: "candidate", Type: stmt.Text}}},
		{ID: sealCandidateAliasRead, Kind: stmt.Read, SQL: `SELECT ordinal FROM metadata_candidate_aliases WHERE candidate_id=? ORDER BY ordinal LIMIT 16`, Params: []stmt.Param{{Name: "candidate", Type: stmt.Text}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.enrichment"
		defs[i].CallSites = []string{"writer/enrichment_source_seal_candidates.go:retireSealCandidates", "writer/enrichment_source_seal_candidates.go:sealCandidatesRetired"}
		defs[i].Cohorts = []stmt.Cohort{{Name: "absent_candidate_keys", Bindings: missingSealBindings(defs[i].Params)}}
	}
	return enrichmentStagePlanHints(defs)
}

type candidateRetirementProgress struct {
	removedEvidence                        sql.NullString
	reasons, aliases, candidates, revision int64
	complete                               bool
}

func candidateChildKeys(ctx context.Context, turn *commandTurn, id stmt.ID, candidate string) ([]int64, error) {
	step, err := sealStep(turn, id)
	if err != nil {
		return nil, err
	}
	rows, err := turn.query(ctx, step, candidate)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	keys := make([]int64, 0, sealCandidateChildQuantum)
	for rows.Next() {
		var n int64
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		if len(keys) == sealCandidateChildQuantum {
			return nil, ErrInvalid
		}
		keys = append(keys, n)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return keys, nil
}
func firstSealCandidate(ctx context.Context, turn *commandTurn, owner string) (string, bool, error) {
	step, err := sealStep(turn, sealCandidateReady)
	if err != nil {
		return "", false, err
	}
	row, err := turn.queryRow(ctx, step, owner)
	if err != nil {
		return "", false, err
	}
	var id string
	var evidence sql.NullString
	if err := row.Scan(&id, &evidence); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", false, nil
		}
		return "", false, err
	}
	return id, true, nil
}

// FIRST uses the adopted zero-candidate option only with the independently
// selected native binding. Bounded child-first PK writes, then covering R CAS;
// receipt/last/accounting/terminal and whole rollback remain W-owned.
func retireSealCandidates(ctx context.Context, turn *commandTurn, scope sealOwnerScope) (out candidateRetirementProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err := scope.checkLease(turn); err != nil {
		return out, err
	}
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return out, err
	}
	candidate, found, err := firstSealCandidate(ctx, turn, scope.ownerID)
	if err != nil {
		return out, err
	}
	out.revision = scope.sourceRevision
	if !found {
		out.complete = true
		return out, nil
	}
	for i, pair := range []struct{ read, write stmt.ID }{{sealCandidateReasonRead, sealCandidateReasons}, {sealCandidateAliasRead, sealCandidateAliases}} {
		keys, err := candidateChildKeys(ctx, turn, pair.read, candidate)
		if err != nil {
			return out, err
		}
		for _, key := range keys {
			if err := execSealOne(ctx, turn, pair.write, candidate, key); err != nil {
				return out, err
			}
		}
		if i == 0 {
			out.reasons = int64(len(keys))
		} else {
			out.aliases = int64(len(keys))
		}
	}
	reasons, err := candidateChildKeys(ctx, turn, sealCandidateReasonRead, candidate)
	if err != nil {
		return out, err
	}
	aliases, err := candidateChildKeys(ctx, turn, sealCandidateAliasRead, candidate)
	if err != nil {
		return out, err
	}
	if len(reasons) == 0 && len(aliases) == 0 {
		step, err := sealStep(turn, sealCandidateParent)
		if err != nil {
			return out, err
		}
		row, err := turn.queryRow(ctx, step, candidate, scope.ownerID)
		if err != nil {
			return out, err
		}
		if err := row.Scan(&out.removedEvidence); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return out, ErrConflict
			}
			return out, err
		}
		out.candidates = 1
	}
	if out.reasons+out.aliases+out.candidates > 0 {
		step, err := sealStep(turn, sealCandidateAdvance)
		if err != nil {
			return out, err
		}
		row, err := turn.queryRow(ctx, step, scope.values()...)
		if err != nil {
			return out, err
		}
		if err := row.Scan(&out.revision); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return out, ErrConflict
			}
			return out, err
		}
		if out.revision != scope.sourceRevision+1 {
			return out, ErrConflict
		}
	}
	scope.sourceRevision = out.revision
	out.complete, err = sealCandidatesRetired(ctx, turn, scope)
	return out, err
}
func sealCandidatesRetired(ctx context.Context, turn *commandTurn, scope sealOwnerScope) (bool, error) {
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return false, err
	}
	_, found, err := firstSealCandidate(ctx, turn, scope.ownerID)
	return !found, err
}
