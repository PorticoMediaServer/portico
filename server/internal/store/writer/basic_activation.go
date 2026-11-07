package writer

import (
	"context"
	"database/sql"
	"math"
	"unicode/utf8"
)

type basicActivationEffects struct {
	source                                 basicStreamFactStage
	changeRevision, familyMask, shapeID    int64
	ownerObject, ownerRevision             sql.NullString // separate accepted choice, not the probe witness
	invalidationRevision, fileSizeRevision int64
}
type basicActivatedAsset struct {
	assetID, infoEpoch, changeRevision, phase, available int64
	ownerObject, ownerRevision                           sql.NullString
}

// activateBasicAsset is only the native activation effect INSIDE the complete
// BasicAccept turn. The asset's publication fence remains set. The caller must
// finish the complete holder/resource/aggregate cohort and original receipt in
// this SAME transaction before clearing that fence or returning BasicResult.
// In particular this intermediate return is never a ready/Applied receipt.
func activateBasicAsset(ctx context.Context, turn *commandTurn, in basicActivationEffects) (value basicActivatedAsset, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || in.source.expectedEpoch == math.MaxInt64 || in.changeRevision <= 0 || in.changeRevision == math.MaxInt64 || in.shapeID <= 0 || (in.familyMask != 7 && in.familyMask != 511) {
		return value, ErrInvalid
	}
	if in.ownerObject.Valid != in.ownerRevision.Valid || (!in.ownerObject.Valid && (in.ownerObject.String != "" || in.ownerRevision.String != "")) || (in.ownerObject.Valid && (in.ownerObject.String == "" || in.ownerRevision.String == "" || len(in.ownerObject.String) > maxCanonicalRequestBytes || len(in.ownerRevision.String) > maxCanonicalRequestBytes || !utf8.ValidString(in.ownerObject.String) || !utf8.ValidString(in.ownerRevision.String))) {
		return value, ErrInvalid
	}
	// FIRST ordinary/non-disc scope preserves actual NULL/NULL. A retained
	// disc choice is a separately admitted scope, never cleared by this probe.
	if in.ownerObject.Valid {
		return value, &Refusal{Code: RefusalUnqualified}
	}
	if in.invalidationRevision <= 0 || in.fileSizeRevision <= 0 {
		return value, ErrInvalid
	}
	if err = checkBasicInputAtPhase(ctx, turn, in.source, 3); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicActivationHeader, in.source.changeID)
	if err != nil {
		return value, err
	}
	var revision, mask, copyFamily, copyPhase, copyID, copyAux, leaseOwner, leaseGeneration, leaseExpires int64
	var target, available sql.NullInt64
	var object, objectRevision sql.NullString
	var key, key2, key3 string
	if err = row.Scan(&revision, &mask, &target, &available, &object, &objectRevision, &copyFamily, &copyPhase, &copyID, &copyAux, &key, &key2, &key3, &leaseOwner, &leaseGeneration, &leaseExpires); err != nil {
		return value, basicStageError(err)
	}
	if revision != in.changeRevision || mask != in.familyMask || !target.Valid || target.Int64 != in.shapeID || !available.Valid || object != in.ownerObject || objectRevision != in.ownerRevision || copyFamily != 0 || copyPhase != 0 || copyID != 0 || copyAux != 0 || key != "" || key2 != "" || key3 != "" || leaseOwner != 0 || leaseGeneration != 0 || leaseExpires != 0 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicActivationFact, in.source.assetToken)
	if err != nil {
		return value, err
	}
	var factRevision, size, modified, detail int64
	var fingerprint string
	if err = row.Scan(&factRevision, &size, &modified, &fingerprint, &detail); err != nil {
		return value, basicStageError(err)
	}
	if factRevision != in.source.factRevision || size != in.source.size || modified != in.source.modifiedNS || fingerprint != in.source.fingerprint || detail != in.source.detailVersion {
		return value, ErrConflict
	}
	var ownerValue, revisionValue any
	if object.Valid {
		ownerValue, revisionValue = object.String, objectRevision.String
	}
	row, err = basicStageRead(ctx, turn, basicActivationAssetCAS, in.source.expectedEpoch, available.Int64, ownerValue, revisionValue,
		in.source.assetID, in.source.assetToken, in.source.libraryID, in.source.expectedEpoch, in.source.size, in.source.modifiedNS, int64(1), in.fileSizeRevision, in.invalidationRevision)
	if err != nil {
		return value, err
	}
	var pending, actualSizeRevision, actualInvalidationRevision int64
	if err = row.Scan(&value.assetID, &value.infoEpoch, &value.available, &value.ownerObject, &value.ownerRevision, &pending, &actualSizeRevision, &actualInvalidationRevision); err != nil {
		return value, basicStageError(err)
	}
	if value.assetID != in.source.assetID || value.infoEpoch != in.source.expectedEpoch+1 || value.available != available.Int64 || value.ownerObject != object || value.ownerRevision != objectRevision || pending != 1 || actualSizeRevision != in.fileSizeRevision || actualInvalidationRevision != in.invalidationRevision {
		return value, ErrConflict
	}
	changeArgs := []any{int64(4), in.changeRevision + 1, in.source.changeID, in.source.assetID, in.source.assetToken, int64(0), int64(3), in.source.expectedEpoch, in.source.inputRevision, in.familyMask, in.source.witnessFingerprint[:], in.changeRevision, in.shapeID, available.Int64}
	changeArgs = append(changeArgs, int64(0), int64(0), int64(0), int64(0), "", "", "", int64(0), int64(0), int64(0), "")
	row, err = basicStageRead(ctx, turn, basicActivationChangeCAS, changeArgs...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.changeRevision, &value.phase); err != nil {
		return value, basicStageError(err)
	}
	if value.changeRevision != in.changeRevision+1 || value.phase != 4 {
		return value, ErrConflict
	}
	return value, nil
}
