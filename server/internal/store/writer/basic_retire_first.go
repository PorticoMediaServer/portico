package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"
)

// Old immutable input and current ready-asset epoch have different meanings.
// The enclosing live-run Stage command freezes both; neither is inferred from
// the other when reading native state or producing its historical result.
type basicRetireFirstEffects struct {
	source                                               basicStreamFactStage
	currentAssetEpoch, changeRevision, shapeID, entityID int64
	counts                                               [9]int64
}
type basicRetiredFirstInput struct {
	changeID, expectedEpoch, currentAssetEpoch, deleted, refunded int64
}

func basicRetireFirstCharge(in basicRetireFirstEffects) (int64, error) {
	s := in.source
	if s.changeID <= 0 || s.inputRevision <= 0 || s.libraryID <= 0 || s.assetID <= 0 || s.expectedEpoch <= 0 || s.expectedEpoch == math.MaxInt64 || in.currentAssetEpoch != s.expectedEpoch+1 ||
		in.changeRevision <= 0 || in.shapeID <= 0 || in.entityID <= 0 || s.sourceGeneration <= 0 || s.policyRevision <= 0 || s.factRevision <= 0 || s.detailVersion < 0 || s.size < 0 || s.witnessFingerprint == ([32]byte{}) || in.counts[0] != 1 {
		return 0, ErrInvalid
	}
	for _, n := range in.counts {
		if n < 0 {
			return 0, ErrInvalid
		}
	}
	remaining := maxCanonicalRequestBytes
	for _, v := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint} {
		if len(v) > remaining {
			return 0, ErrCap
		}
		remaining -= len(v)
	}
	for _, v := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint} {
		if v == "" || !utf8.ValidString(v) {
			return 0, ErrInvalid
		}
	}
	// Same retained logical charges as BEGIN and family1 staging. These are
	// actual rows deleted below; this is not a physical memory allowance.
	charge := int64(344 + 32 + 224 + 32 + 9*176 + 192)
	for _, v := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.assetToken, s.fingerprint} {
		if int64(len(v)) > 33554432-charge {
			return 0, ErrCap
		}
		charge += int64(len(v))
	}
	return charge, nil
}

func basicRetireHeaderArgs(in basicRetireFirstEffects) []any {
	s := in.source
	return []any{s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], in.changeRevision, in.shapeID, in.entityID, in.entityID}
}

func basicRetireWitnessArgs(s basicStreamFactStage) []any {
	return []any{s.changeID, int64(0), int64(0), s.runID, s.sourceID, s.sourceGeneration, s.incarnation, s.objectID, s.objectRevision, s.policyRevision, int64(0), s.witnessFingerprint[:]}
}

func checkBasicRetireWitness(ctx context.Context, turn *commandTurn, s basicStreamFactStage) error {
	row, err := basicStageRead(ctx, turn, basicRetireWitnessMatch, basicRetireWitnessArgs(s)...)
	if err != nil {
		return err
	}
	var matched int64
	if err = row.Scan(&matched); err != nil {
		return basicStageError(err)
	}
	if matched != 1 {
		return ErrConflict
	}
	return nil
}

// Final fixed-size retirement after bounded media-prefix cleanup. Only the
// enclosing Stage command supplies live-run disclosure, lease, memory, receipt
// and whole-command rollback. No ACK refund, parent cascade, serving fact
// deletion, COMMIT, Outcome or offline/closed-run authority is introduced.
func retireBasicFirstInput(ctx context.Context, turn *commandTurn, in basicRetireFirstEffects) (value basicRetiredFirstInput, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil {
		return value, ErrInvalid
	}
	charge, err := basicRetireFirstCharge(in)
	if err != nil {
		return value, err
	}
	s := in.source
	row, err := basicStageRead(ctx, turn, basicRetireHeader, basicRetireHeaderArgs(in)...)
	if err != nil {
		return value, err
	}
	var id int64
	if err = row.Scan(&id); err != nil {
		return value, basicStageError(err)
	}
	if id != s.changeID {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicRetireAsset, s.assetID, s.libraryID, s.assetToken, in.currentAssetEpoch)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.currentAssetEpoch); err != nil {
		return value, basicStageError(err)
	}
	if value.currentAssetEpoch != in.currentAssetEpoch {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicRetireLink, in.entityID, s.assetID, s.libraryID, in.shapeID)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&id); err != nil {
		return value, basicStageError(err)
	}
	if id != 1 {
		return value, ErrConflict
	}
	if err = checkBasicRetireWitness(ctx, turn, s); err != nil {
		return value, err
	}
	// These constant-valued bounded points allocate no unknown child payload.
	// They cover every other adopted inbound header edge before parent DELETE.
	for _, point := range basicRetireEmptyPoints() {
		row, err = basicStageRead(ctx, turn, point, s.changeID, int64(1))
		if err != nil {
			return value, err
		}
		err = row.Scan(&id)
		if err == nil {
			return value, ErrConflict
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return value, err
		}
	}
	rows, err := basicRows(ctx, turn, basicRetireFamilyKeys, s.changeID, int64(10))
	if err != nil {
		return value, err
	}
	for family := int64(1); family <= 9; family++ {
		if !rows.Next() {
			return value, errors.Join(ErrConflict, rows.Err(), rows.Close())
		}
		if err = rows.Scan(&id); err != nil {
			return value, errors.Join(err, rows.Close())
		}
		if id != family {
			return value, errors.Join(ErrConflict, rows.Close())
		}
	}
	if rows.Next() {
		return value, errors.Join(ErrConflict, rows.Close())
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return value, err
	}
	rows, err = basicRows(ctx, turn, basicRetireWitnessKeys, s.changeID, int64(2))
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, []any{int64(0)}); err != nil {
		return value, err
	}
	rows, err = basicRows(ctx, turn, basicRetireStreamFactKeys, s.changeID, int64(2))
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, []any{int64(0)}); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicRetireShapeRefs, in.shapeID)
	if err != nil {
		return value, err
	}
	var refs int64
	if err = row.Scan(&refs); err != nil {
		return value, basicStageError(err)
	}
	if refs < 2 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return value, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return value, basicStageError(err)
	}
	if cap != 33554432 || used < charge || used > cap {
		return value, ErrConflict
	}
	for n, count := range in.counts {
		args := []any{s.changeID, int64(n + 1), int64(0), count, count, int64(1)}
		rows, err = basicRows(ctx, turn, basicRetireDeleteFamily, args...)
		if err != nil {
			return value, err
		}
		if err = basicMediaExactRow(rows, args); err != nil {
			return value, err
		}
		value.deleted++
	}
	args := []any{s.changeID, s.assetToken, int64(0), s.factRevision, s.size, s.modifiedNS, s.fingerprint, s.detailVersion}
	rows, err = basicRows(ctx, turn, basicRetireDeleteStreamFact, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, args); err != nil {
		return value, err
	}
	value.deleted++
	args = basicRetireWitnessArgs(s)
	rows, err = basicRows(ctx, turn, basicRetireDeleteWitness, args...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(rows, args); err != nil {
		return value, err
	}
	value.deleted++
	row, err = basicStageRead(ctx, turn, basicRetireDeleteHeader, basicRetireHeaderArgs(in)...)
	if err != nil {
		return value, err
	}
	var asset, inputRevision, changeRevision, shape int64
	var token string
	var fingerprint []byte
	if err = row.Scan(&value.changeID, &value.expectedEpoch, &asset, &token, &inputRevision, &changeRevision, &shape, &fingerprint); err != nil {
		return value, basicStageError(err)
	}
	if value.changeID != s.changeID || value.expectedEpoch != s.expectedEpoch || asset != s.assetID || token != s.assetToken || inputRevision != s.inputRevision || changeRevision != in.changeRevision || shape != in.shapeID || !bytes.Equal(fingerprint, s.witnessFingerprint[:]) {
		return value, ErrConflict
	}
	value.deleted++
	row, err = basicStageRead(ctx, turn, basicRetireShapeRelease, refs-1, in.shapeID, refs)
	if err != nil {
		return value, err
	}
	var remainingRefs int64
	if err = row.Scan(&id, &remainingRefs); err != nil {
		return value, basicStageError(err)
	}
	if id != in.shapeID || remainingRefs != refs-1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used-charge, int64(1), used, cap)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&id); err != nil {
		return value, basicStageError(err)
	}
	if id != used-charge {
		return value, ErrConflict
	}
	value.refunded = charge
	return value, nil
}
