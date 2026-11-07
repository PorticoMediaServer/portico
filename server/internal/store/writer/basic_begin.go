package writer

import (
	"bytes"
	"context"
	"errors"
	"unicode/utf8"
)

// FIRST complete ordinary input, allocation independent. The command request
// has no generated change ID/input revision. W resolves original history and
// proves current source/run/lease authority before these inert native effects.
type basicBeginEffects struct {
	source basicStreamFactStage
	shape  ScanShape
	counts [9]int64
}
type basicBegunInput struct {
	source         basicStreamFactStage
	changeRevision int64
}

func beginBasicInput(ctx context.Context, turn *commandTurn, in basicBeginEffects, appliedMS int64) (value basicBegunInput, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	s := in.source
	if ctx == nil || turn == nil || appliedMS < 0 || s.changeID != 0 || s.inputRevision != 0 || s.assetID <= 0 || s.libraryID <= 0 || s.expectedEpoch <= 0 || s.sourceGeneration <= 0 || s.policyRevision <= 0 || s.factRevision <= 0 || s.size < 0 || s.detailVersion < 0 || s.witnessFingerprint == ([32]byte{}) {
		return value, ErrInvalid
	}
	if err = validateBasicOrdinaryCounts(s.libraryID, in.shape, in.counts); err != nil {
		return value, err
	}
	// Exact adopted retained header + ONE witness + nine family rows. This is
	// logical storage debt, not a guessed memory grant or native W reserve.
	remaining := int64(33554432)
	for _, text := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint} {
		if int64(len(text)) > remaining {
			return value, ErrCap
		}
		remaining -= int64(len(text))
		if text == "" || !utf8.ValidString(text) {
			return value, ErrInvalid
		}
	}
	charge := int64(344 + 32 + 224 + 32 + 9*176)
	for _, text := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision} {
		if int64(len(text)) > 33554432-charge {
			return value, ErrCap
		}
		charge += int64(len(text))
		if text == "" || !utf8.ValidString(text) {
			return value, ErrInvalid
		}
	}
	row, err := basicStageRead(ctx, turn, basicStageAsset, s.assetID)
	if err != nil {
		return value, err
	}
	var library, size, modified, epoch int64
	var token string
	if err = row.Scan(&library, &token, &size, &modified, &epoch); err != nil {
		return value, basicStageError(err)
	}
	if library != s.libraryID || token != s.assetToken || size != s.size || modified != s.modifiedNS || epoch != s.expectedEpoch {
		return value, ErrConflict
	}
	if err = checkBasicBeginFirst(ctx, turn, s); err != nil {
		return value, err
	}
	existing, err := basicRows(ctx, turn, basicBeginExisting, s.assetID, s.assetToken, int64(3))
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, existing.Close()) }()
	if existing.Next() {
		return value, ErrConflict
	}
	if err = existing.Err(); err != nil {
		return value, err
	}
	if err = existing.Close(); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return value, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return value, basicStageError(err)
	}
	if cap != 33554432 || used < 0 || used > cap || charge > cap-used {
		return value, ErrCap
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used+charge, int64(1), used, cap)
	if err != nil {
		return value, err
	}
	var charged int64
	if err = row.Scan(&charged); err != nil {
		return value, basicStageError(err)
	}
	if charged != used+charge {
		return value, ErrConflict
	}
	rows, err := basicRows(ctx, turn, basicBeginChangeInsert, s.assetID, s.assetToken, int64(0), int64(0), s.expectedEpoch, int64(1), int64(511), s.witnessFingerprint[:], appliedMS, int64(1))
	if err != nil {
		return value, err
	}
	if !rows.Next() {
		_ = rows.Close()
		return value, ErrConflict
	}
	var changeID, inputRevision, jobRevision int64
	var assetID, slot, phase, expectedEpoch, familyMask, createdMS int64
	var assetToken string
	var fingerprint []byte
	if err = rows.Scan(&changeID, &assetID, &assetToken, &slot, &phase, &expectedEpoch, &inputRevision, &familyMask, &fingerprint, &createdMS, &jobRevision); err != nil {
		_ = rows.Close()
		return value, err
	}
	if rows.Next() {
		_ = rows.Close()
		return value, ErrConflict
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return value, err
	}
	if err = rows.Close(); err != nil {
		return value, err
	}
	if changeID <= 0 || assetID != s.assetID || assetToken != s.assetToken || slot != 0 || phase != 0 || expectedEpoch != s.expectedEpoch || inputRevision != 1 || familyMask != 511 || !bytes.Equal(fingerprint, s.witnessFingerprint[:]) || createdMS != appliedMS || jobRevision != 1 {
		return value, ErrConflict
	}
	witnessArgs := []any{changeID, int64(0), int64(0), s.runID, s.sourceID, s.sourceGeneration, s.incarnation, s.objectID, s.objectRevision, s.policyRevision, int64(0), s.witnessFingerprint[:]}
	witness, err := basicRows(ctx, turn, basicBeginWitnessInsert, witnessArgs...)
	if err != nil {
		return value, err
	}
	if err = basicMediaExactRow(witness, witnessArgs); err != nil {
		return value, err
	}
	// Exactly nine uses of one finite literal statement; no dynamic SQL or
	// selector/cursor alias. All rows and logical debt share this transaction.
	for n, count := range in.counts {
		args := []any{changeID, int64(n + 1), int64(0), count, int64(0), int64(0)}
		family, err := basicRows(ctx, turn, basicBeginFamiliesInsert, args...)
		if err != nil {
			return value, err
		}
		if err = basicMediaExactRow(family, args); err != nil {
			return value, err
		}
	}
	s.changeID = changeID
	s.inputRevision = inputRevision
	return basicBegunInput{s, jobRevision}, nil
}

// This validates the original complete ordinary manifest BEFORE any cleanup or
// SQL. Sealed native manifests must separately match these captured counts.
func validateBasicOrdinaryCounts(libraryID int64, shape ScanShape, counts [9]int64) error {
	if err := ValidateScanShape(shape); err != nil {
		return err
	}
	if shape.LibraryID != libraryID || shape.OrdinaryOwners != 1 || len(shape.CUEOrdinals) != 0 || len(shape.EpisodeIDs) != 0 {
		return ErrInvalid
	}
	for n, count := range counts {
		limit := int64(1)
		if n == 1 || n == 2 {
			limit = 3 * MaxStreamsPerClass
		} else if n >= 3 {
			limit = basicMediaFamilyLimit(int64(n + 1))
		}
		if count < 0 || count > limit {
			return ErrCap
		}
	}
	if counts[0] != 1 || counts[1] != int64(len(shape.Streams)) || counts[2] != counts[1] || counts[3] != 1 || counts[4] > int64(shape.RawChapterCount) || counts[5] != 1 || counts[8] != 1 {
		return ErrInvalid
	}
	return nil
}
