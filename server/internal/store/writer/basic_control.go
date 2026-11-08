package writer

import (
	"context"
	"database/sql"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

type basicFreshFacts struct {
	revision, discovered, analyzed, warnings, processed int64
}

// These are FRESH native facts only. W must resolve original history and prove
// current disclosure before this helper. It never hides a historical receipt
// behind current source, object, policy, phase, lease or overflow checks.
func readBasicFreshFacts(ctx context.Context, turn *commandTurn, identity CommandIdentity, original basicOriginalScope, source basicStreamFactStage) (value basicFreshFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	if err = basicOriginalPreflight(identity, original, source); err != nil {
		return value, err
	}
	// FIRST supports progressive Basic while Inventory is still listing and
	// after its durable traversal frontier has reached verifying. This does
	// not itself complete verification, reconcile absence or finish the run.
	if original.run.phase != "inventory" && original.run.phase != "verifying" {
		return value, &Refusal{Code: RefusalUnqualified}
	}
	if original.run.revision == math.MaxInt64 {
		return value, ErrCap
	}
	s, r, scope := original.source, original.run, original.scope
	row, err := basicStageRead(ctx, turn, basicControlRun, basicControlRunArgs(identity, original)...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.revision, &value.discovered, &value.analyzed, &value.warnings); err != nil {
		return value, basicStageError(err)
	}
	if value.revision != r.revision || value.discovered < 0 || value.analyzed < 0 || value.warnings < 0 {
		return value, ErrConflict
	}
	for _, point := range []struct {
		id   stmt.ID
		args []any
		want string
	}{
		{basicControlSource, []any{s.id, scope.libraryKey, s.generation, s.incarnation, s.rootIdentity, string(s.resolvedRoot), basicBool(s.followSymlinks)}, s.id},
		{basicControlActive, []any{s.id}, scope.runID},
		{basicControlObject, []any{source.objectID, s.id, source.assetToken, s.incarnation, source.objectRevision, source.size, source.modifiedNS, scope.runID}, source.objectID},
	} {
		row, err = basicStageRead(ctx, turn, point.id, point.args...)
		if err != nil {
			return value, err
		}
		var actual string
		if err = row.Scan(&actual); err != nil {
			return value, basicStageError(err)
		}
		if actual != point.want {
			return value, ErrConflict
		}
	}
	row, err = basicStageRead(ctx, turn, basicControlLibrary, scope.libraryID, scope.libraryKey)
	if err != nil {
		return value, err
	}
	var id int64
	if err = row.Scan(&id); err != nil {
		return value, basicStageError(err)
	}
	if id != scope.libraryID {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicControlPolicy, scope.libraryKey, r.policyRevision)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&id); err != nil {
		return value, basicStageError(err)
	}
	if id != r.policyRevision {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicControlJob, scope.runID, scope.libraryKey)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.processed); err != nil {
		return value, basicStageError(err)
	}
	if value.processed < 0 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicControlAsset, source.assetID, scope.libraryID, source.assetToken, source.size, source.modifiedNS, source.expectedEpoch)
	if err != nil {
		return value, err
	}
	var owner, revision sql.NullString
	if err = row.Scan(&id, &owner, &revision); err != nil {
		return value, basicStageError(err)
	}
	if id != source.assetID {
		return value, ErrConflict
	}
	if owner.Valid || revision.Valid {
		return value, &Refusal{Code: RefusalUnqualified}
	}
	return value, nil
}

func basicControlRunArgs(identity CommandIdentity, in basicOriginalScope) []any {
	s, r, scope := in.source, in.run, in.scope
	return []any{scope.runID, identity.Incarnation[:], scope.libraryID, scope.libraryKey, string(s.resolvedRoot), basicBool(s.followSymlinks),
		s.id, s.generation, s.incarnation, s.rootIdentity, r.policyRevision, r.phase, r.scanMode, r.leaseOwner, r.leaseGeneration, r.leaseExpiresMS,
		r.revision, r.lastSequence, r.acknowledgedSequence}
}

// A Stage/Seal consumes exactly one command sequence and run revision. It does
// not advance an Inventory directory or claim any file was analyzed/processed.
func advanceBasicControlRun(ctx context.Context, turn *commandTurn, identity CommandIdentity, in basicOriginalScope, facts basicFreshFacts) (value int64, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || in.run.lastSequence != identity.Sequence-1 || in.run.acknowledgedSequence != in.run.lastSequence || facts.revision != in.run.revision || facts.revision <= 0 || facts.revision == math.MaxInt64 || facts.discovered < 0 || facts.analyzed < 0 || facts.warnings < 0 || identity.Sequence <= 0 {
		return 0, ErrInvalid
	}
	args := []any{facts.revision + 1, identity.Sequence}
	args = append(args, basicControlRunArgs(identity, in)...)
	args = append(args, facts.discovered, facts.analyzed, facts.warnings)
	row, err := basicStageRead(ctx, turn, basicControlRunCAS, args...)
	if err != nil {
		return 0, err
	}
	var sequence, discovered, analyzed, warnings int64
	if err = row.Scan(&value, &sequence, &discovered, &analyzed, &warnings); err != nil {
		return 0, basicStageError(err)
	}
	if value != facts.revision+1 || sequence != identity.Sequence || discovered != facts.discovered || analyzed != facts.analyzed || warnings != facts.warnings {
		return 0, ErrConflict
	}
	return value, nil
}

// Complete actual native post-state, not a synthetic stage-row continuation.
func readBasicControlPostState(ctx context.Context, turn *commandTurn, source basicStreamFactStage) (value BasicContinuation, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || source.changeID <= 0 || source.inputRevision <= 0 || source.expectedEpoch <= 0 || source.assetID <= 0 {
		return value, ErrInvalid
	}
	row, err := basicStageRead(ctx, turn, basicControlPostState, source.changeID, source.assetID, source.assetToken, source.inputRevision, source.expectedEpoch)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.NextFamily, &value.NextCopyPhase, &value.NextCopyID, &value.NextCopyAux, &value.NextCopyKey, &value.NextCopyKey2, &value.NextCopyKey3, &value.AfterEntity); err != nil {
		return value, basicStageError(err)
	}
	return value, nil
}

// W supplies its closed purpose-specific digest over the complete saved native
// projection. This helper is only literal receipt persistence in the SAME turn;
// it has no encoder, outcome mapping, authority or independent COMMIT.
func writeBasicControlReceipt(ctx context.Context, turn *commandTurn, identity CommandIdentity, request RequestDigest, result BasicResult) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || identity.Sequence <= 0 || request == (RequestDigest{}) || result.Digest == (ResultDigest{}) ||
		result.RunID == "" || result.AcceptedRunRevision <= 0 || result.AppliedMS < 0 || result.SourceID == "" || result.SourceIncarnation == "" || result.SourceRevision == "" ||
		result.ChangeID <= 0 || result.ExpectedEpoch <= 0 || result.Outcome != (ReceiptOutcome{}) || result.Members == nil || result.Continuation.NextCursor != "" {
		return ErrInvalid
	}
	var kind string
	switch result.Purpose {
	case BasicPurposeStage:
		kind = "basic_stage"
	case BasicPurposeSeal:
		kind = "basic_seal"
	case BasicPurposeAccept:
		kind = "basic_accept"
	default:
		return ErrInvalid
	}
	count := int64(0)
	if result.Purpose == BasicPurposeAccept {
		count = 1
	}
	if result.ResultCount != count || int64(len(result.Members)) != count {
		return ErrInvalid
	}
	c := result.Continuation
	if c.NextFamily < 0 || c.NextCopyPhase < 0 || c.NextCopyID < 0 || c.NextCopyAux < 0 || c.NextCopyAux > 1 || c.AfterEntity < 0 {
		return ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	texts := [...]string{result.RunID, result.SourceID, result.SourceIncarnation, result.SourceRevision, c.NextCopyKey, c.NextCopyKey2, c.NextCopyKey3}
	for _, text := range texts {
		if len(text) > remaining {
			return ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range texts {
		if !utf8.ValidString(text) {
			return ErrInvalid
		}
	}
	step, ok := turn.steps[basicControlReceipt]
	if !ok {
		return invalid("unregistered_basic_step")
	}
	stored, err := turn.exec(ctx, step.statementStep, result.RunID, identity.Incarnation[:], identity.Sequence, identity.OperationID[:], request[:], kind,
		result.AcceptedRunRevision, result.AppliedMS, result.SourceID, result.SourceIncarnation, result.SourceRevision, result.ChangeID, c.NextCursor,
		c.NextFamily, c.NextCopyPhase, c.NextCopyID, c.NextCopyAux, c.NextCopyKey, c.NextCopyKey2, c.NextCopyKey3, c.AfterEntity, result.ExpectedEpoch, result.ResultCount, result.Digest[:])
	if err != nil {
		return err
	}
	n, err := stored.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
