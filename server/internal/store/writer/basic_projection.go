package writer

import (
	"context"
	"math"
	"strings"
	"unicode/utf8"
)

// The issuer supplies the native run/source scope and the producer supplies
// the exact observed object and contained asset path. This is a value carrier,
// not permission to read a library or open a physical file.
type basicAssetObservation struct {
	assetID, libraryID, size, modifiedNS                              int64
	assetPath, objectID, sourceID, incarnation, objectRevision, runID string
}

// Capture BEFORE probing. Later effect CASes restate every independent epoch;
// reading them after the probe could accept an invalidation that raced the IO.
type basicAssetProjection struct {
	assetToken                                                                                string
	before                                                                                    basicProbeScalars
	playbackIncarnation                                                                       [16]byte
	playbackRevision, preparedRevision, fileSizeRevision, invalidationRevision, expectedEpoch int64
}

// Complete input plus native payload and scan/copy scratch. The caller retains
// the original funded preparation while consuming the returned value. No new
// ledger, admission, grant or authority is created by this body.
func basicProjectionRequiredBytes(in basicAssetObservation, octets [5]int64) (uint64, error) {
	remaining := uint64(maxCanonicalRequestBytes)
	for _, s := range [...]string{in.assetPath, in.objectID, in.sourceID, in.incarnation, in.objectRevision, in.runID} {
		if uint64(len(s)) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(len(s))
	}
	for _, n := range octets {
		if n < 0 || uint64(n) > remaining {
			return 0, ErrCap
		}
		remaining -= uint64(n)
	}
	return 16384 + 4*(uint64(maxCanonicalRequestBytes)-remaining), nil
}

// Current disclosure, the original run/source vector and lease eligibility are
// outer dispatcher gates. This helper supplies only the FIRST ordinary native
// asset vector on that SAME exact-purpose, Capture-receipted Basic turn.
func readBasicAssetBeforeProbe(ctx context.Context, turn *commandTurn, purpose BasicPurpose, in basicAssetObservation) (value basicAssetProjection, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	required, err := basicProjectionRequiredBytes(in, [5]int64{})
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, purpose, required); err != nil {
		return value, err
	}
	if in.assetID <= 0 || in.libraryID <= 0 || in.size < 0 || in.assetPath == "" || in.assetPath[0] != '/' || strings.IndexByte(in.assetPath, 0) >= 0 {
		return value, ErrInvalid
	}
	if !utf8.ValidString(in.assetPath) {
		return value, ErrFilenameUnrepresentable
	}
	for _, s := range [...]string{in.objectID, in.sourceID, in.incarnation, in.objectRevision, in.runID} {
		if s == "" || !utf8.ValidString(s) {
			return value, ErrInvalid
		}
	}
	row, err := basicStageRead(ctx, turn, basicProjectionLengths, in.assetID, in.libraryID)
	if err != nil {
		return value, err
	}
	var octets [5]int64
	if err = row.Scan(&octets[0], &octets[1], &octets[2], &octets[3], &octets[4]); err != nil {
		return value, basicStageError(err)
	}
	if octets[4] != 16 {
		return value, ErrConflict
	}
	required, err = basicProjectionRequiredBytes(in, octets)
	if err != nil {
		return value, err
	}
	if err = turn.requireBasicMemory(ctx, purpose, required); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicProjectionAsset, in.assetID, in.libraryID, in.assetPath, in.size, in.modifiedNS)
	if err != nil {
		return value, err
	}
	var incarnation []byte
	if err = row.Scan(&value.assetToken, &value.before.container, &value.before.videoCodec, &value.before.audioCodec, &value.before.width, &value.before.height, &value.before.duration,
		&incarnation, &value.playbackRevision, &value.preparedRevision, &value.fileSizeRevision, &value.invalidationRevision, &value.expectedEpoch); err != nil {
		return value, basicStageError(err)
	}
	for n, s := range [...]string{value.assetToken, value.before.container, value.before.videoCodec, value.before.audioCodec} {
		if int64(len(s)) != octets[n] || !utf8.ValidString(s) {
			return value, ErrConflict
		}
	}
	if value.assetToken == "" || len(incarnation) != 16 || value.before.width < 0 || value.before.height < 0 || value.before.duration < 0 || math.IsNaN(value.before.duration) || math.IsInf(value.before.duration, 0) ||
		value.playbackRevision <= 0 || value.preparedRevision <= 0 || value.fileSizeRevision <= 0 || value.invalidationRevision <= 0 || value.expectedEpoch <= 0 {
		return value, ErrConflict
	}
	copy(value.playbackIncarnation[:], incarnation)
	if value.playbackIncarnation == ([16]byte{}) {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicProjectionObject, in.objectID, in.sourceID, value.assetToken, in.incarnation, in.objectRevision, in.size, in.modifiedNS, in.runID)
	if err != nil {
		return value, err
	}
	var matched int64
	if err = row.Scan(&matched); err != nil {
		return value, basicStageError(err)
	}
	if matched != 1 {
		return value, ErrConflict
	}
	return value, nil
}
