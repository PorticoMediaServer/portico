package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// A bounded physical prefix is a separate original Stage command. Native
// deletion itself is progress; no directory or canonical-copy cursor is reused.
func applyBasicRetirePrefix(ctx context.Context, turn *commandTurn, in basicStageRequest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if in.operation != basicStageRetireInput || in.rows.family < 2 || in.rows.family > 9 || in.start < 0 || in.end <= in.start || in.end-in.start > 8 || in.end > basicRowsCount(in.rows) {
		return ErrInvalid
	}
	if _, err = basicRetireFirstCharge(in.retire); err != nil {
		return err
	}
	s := in.retire.source
	row, err := basicStageRead(ctx, turn, basicRetireHeader, basicRetireHeaderArgs(in.retire)...)
	if err != nil {
		return err
	}
	var matched int64
	if err = row.Scan(&matched); err != nil {
		return basicStageError(err)
	}
	if matched != s.changeID {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicRetireAsset, s.assetID, s.libraryID, s.assetToken, in.retire.currentAssetEpoch)
	if err != nil {
		return err
	}
	if err = row.Scan(&matched); err != nil {
		return basicStageError(err)
	}
	if matched != in.retire.currentAssetEpoch {
		return ErrConflict
	}
	if err = checkBasicRetireWitness(ctx, turn, s); err != nil {
		return err
	}
	row, err = basicStageRead(ctx, turn, basicRetireEmptyPendingLinks, s.changeID, int64(1))
	if err != nil {
		return err
	}
	err = row.Scan(&matched)
	if err == nil {
		return ErrConflict
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	row, err = basicStageRead(ctx, turn, basicStageFamily, s.changeID, in.rows.family)
	if err != nil {
		return err
	}
	var witness, expected, staged, sealed int64
	if err = row.Scan(&witness, &expected, &staged, &sealed); err != nil {
		return basicStageError(err)
	}
	if witness != 0 || sealed != 1 || expected != in.retire.counts[in.rows.family-1] || staged != expected || basicRowsCount(in.rows) != expected {
		return ErrConflict
	}
	family, err := prepareBasicRowsRequest(s, in.rows)
	if err != nil {
		return err
	}
	_, _, err = reclaimBasicMediaRows(ctx, turn, basicMediaReclaimInput{family: family, start: in.start, end: in.end, jobRevision: in.retire.changeRevision, targetShapeID: in.retire.shapeID})
	return err
}

// Already settled input cleanup uses the same live run/source/lease, not the
// old object's continued existence or its preactivation asset epoch. It never
// acquires authority for an offline, closed, retired-library or rebound run.
func readBasicRetireLiveFacts(ctx context.Context, turn *commandTurn, identity CommandIdentity, original basicOriginalScope, source basicStreamFactStage) (value basicFreshFacts, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil {
		return value, ErrInvalid
	}
	if err = basicOriginalPreflight(identity, original, source); err != nil {
		return value, err
	}
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
	for _, point := range []struct {
		id   stmt.ID
		args []any
		want int64
	}{
		{basicControlLibrary, []any{scope.libraryID, scope.libraryKey}, scope.libraryID},
		{basicControlPolicy, []any{scope.libraryKey, r.policyRevision}, r.policyRevision},
	} {
		row, err = basicStageRead(ctx, turn, point.id, point.args...)
		if err != nil {
			return value, err
		}
		var actual int64
		if err = row.Scan(&actual); err != nil {
			return value, basicStageError(err)
		}
		if actual != point.want {
			return value, ErrConflict
		}
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
	return value, nil
}

// The DELETE RETURNING values, not a later missing CJR, establish the result.
// Shared history can resolve this header after the staging rows are gone.
func completeBasicRetireControl(ctx context.Context, turn *commandTurn, command *basicStageCommand, facts basicFreshFacts, appliedMS int64, retired basicRetiredFirstInput) (value BasicResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if command == nil || !command.valid || command.request.operation != basicStageRetireInput || retired.deleted != 12 || retired.refunded <= 0 || retired.changeID != command.request.retire.source.changeID || retired.expectedEpoch != command.request.retire.source.expectedEpoch || retired.currentAssetEpoch != command.request.retire.currentAssetEpoch {
		return value, ErrConflict
	}
	original, s := command.request.original, command.request.retire.source
	runRevision, err := advanceBasicControlRun(ctx, turn, command.identity, original, facts)
	if err != nil {
		return value, err
	}
	value = BasicResult{Purpose: BasicPurposeStage, RunID: original.scope.runID, AcceptedRunRevision: runRevision, AppliedMS: appliedMS,
		SourceID: s.sourceID, SourceIncarnation: s.incarnation, SourceRevision: s.objectRevision, ChangeID: retired.changeID, ExpectedEpoch: retired.expectedEpoch, Members: []InventoryMember{}}
	value.Digest, err = basicResultDigest(command.identity, command.digest, value)
	if err != nil {
		return value, err
	}
	if err = writeBasicControlReceipt(ctx, turn, command.identity, command.digest, value); err != nil {
		return value, err
	}
	return value, nil
}
