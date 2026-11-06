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
	sealCandidateChildQuantum         = 16 // physical write quantum, not a retained-input cap
)

func enrichmentCandidateRetirementDefinitions() []stmt.Definition {
	// Closed FIRST chooses the ADOPTED zero-candidate path: actual primary/
	// selected binding is rechecked, then ALL reasons/aliases/parents retire.
	// No unbounded empty child rows survive the tight proof-closure budget.
	first := `SELECT c.id FROM metadata_candidates c WHERE c.operation_id=? ORDER BY c.id LIMIT 1`
	guard := `EXISTS(SELECT 1 FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate + `)`
	defs := []stmt.Definition{}
	for _, pair := range []struct {
		id    stmt.ID
		table string
	}{{sealCandidateReasons, "metadata_candidate_reasons"}, {sealCandidateAliases, "metadata_candidate_aliases"}} {
		defs = append(defs, stmt.Definition{ID: pair.id, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_source_seal_candidates.go:retireSealCandidates"},
			SQL:       `DELETE FROM ` + pair.table + ` WHERE (candidate_id,ordinal) IN (SELECT candidate_id,ordinal FROM ` + pair.table + ` WHERE candidate_id=(` + first + `) ORDER BY ordinal LIMIT 16) AND ` + guard,
			Params:    append([]stmt.Param{{Name: "candidate_owner", Type: stmt.Text}}, sealScopeParams()...)})
	}
	defs = append(defs, stmt.Definition{ID: sealCandidateParent, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_candidates.go:retireSealCandidates"},
		SQL: `DELETE FROM metadata_candidates WHERE id=(` + first + `) AND NOT EXISTS(SELECT 1 FROM metadata_candidate_reasons r WHERE r.candidate_id=metadata_candidates.id)
 AND NOT EXISTS(SELECT 1 FROM metadata_candidate_aliases a WHERE a.candidate_id=metadata_candidates.id) AND ` + guard + ` RETURNING evidence_id`,
		Params: append([]stmt.Param{{Name: "candidate_owner", Type: stmt.Text}}, sealScopeParams()...)},
		stmt.Definition{ID: sealCandidateAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_source_seal_candidates.go:retireSealCandidates"},
			SQL:       `UPDATE metadata_provider_operations AS o SET source_content_revision=source_content_revision+1 WHERE ` + sealOwnerPredicate + ` RETURNING source_content_revision`, Params: sealScopeParams()},
		stmt.Definition{ID: sealCandidateReady, Owner: "writer.enrichment", Kind: stmt.Read, Plan: stmt.Plan{Hot: true},
			CallSites: []string{"writer/enrichment_source_seal_candidates.go:sealCandidatesRetired"},
			SQL:       `SELECT NOT EXISTS(SELECT 1 FROM metadata_candidates c WHERE c.operation_id=o.id) FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate, Params: sealScopeParams()})
	for i := range defs {
		args := sealScopeCohort()
		if defs[i].ID == sealCandidateReasons || defs[i].ID == sealCandidateAliases || defs[i].ID == sealCandidateParent {
			args = append([]any{"operation-one"}, args...)
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "native_candidate_binding", Bindings: args}}
	}
	return defs
}

type candidateRetirementProgress struct {
	removedEvidence                        sql.NullString // physical unlink fact, never an accounting refund
	reasons, aliases, candidates, revision int64
	complete                               bool
}

// Private physical effect body only. The enclosing W command owns original
// receipt resolution, complete authority/admission, last/ACK and commit/rollback.
// A successful partial chunk is durable ONLY together with that typed receipt.
func retireSealCandidates(ctx context.Context, turn *commandTurn, scope sealOwnerScope) (out candidateRetirementProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err := scope.checkLease(turn); err != nil {
		return out, err
	}
	args := append([]any{scope.ownerID}, scope.values()...)
	counts := []*int64{&out.reasons, &out.aliases}
	for i, id := range []stmt.ID{sealCandidateReasons, sealCandidateAliases} {
		step, err := sealStep(turn, id)
		if err != nil {
			return out, err
		}
		result, err := turn.exec(ctx, step, args...)
		if err != nil {
			return out, err
		}
		changed, err := result.RowsAffected()
		if err != nil {
			return out, err
		}
		bound := int64(sealCandidateChildQuantum)
		if changed < 0 || changed > bound {
			return out, invalid("candidate_retirement_quantum")
		}
		*counts[i] = changed
	}
	step, err := sealStep(turn, sealCandidateParent)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, args...)
	if err != nil {
		return out, err
	}
	if err := row.Scan(&out.removedEvidence); err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
	} else {
		out.candidates = 1
	}

	out.revision = scope.sourceRevision
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
	step, err := sealStep(turn, sealCandidateReady)
	if err != nil {
		return false, err
	}
	row, err := turn.queryRow(ctx, step, scope.values()...)
	if err != nil {
		return false, err
	}
	var ready int64
	if err := row.Scan(&ready); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrConflict
		}
		return false, err
	}
	if ready != 0 && ready != 1 {
		return false, ErrInvalid
	}
	return ready == 1, nil
}
