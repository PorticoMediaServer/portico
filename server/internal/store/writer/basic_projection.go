package writer

import (
	"context"
	"math"
	"path/filepath"
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
	observation                                                                               basicAssetObservation
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
	if in.assetID <= 0 || in.libraryID <= 0 || in.size < 0 || !filepath.IsAbs(in.assetPath) || strings.IndexByte(in.assetPath, 0) >= 0 {
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
		&incarnation, &value.playbackRevision, &value.preparedRevision, &value.invalidationRevision, &value.fileSizeRevision, &value.expectedEpoch); err != nil {
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
	value.observation = basicAssetObservation{in.assetID, in.libraryID, in.size, in.modifiedNS,
		strings.Clone(in.assetPath), strings.Clone(in.objectID), strings.Clone(in.sourceID), strings.Clone(in.incarnation), strings.Clone(in.objectRevision), strings.Clone(in.runID)}
	return value, nil
}

// Derive the effect input from the PRE-probe native capture, not from counters
// reread after IO or supplied by a producer. Fresh SQL still restates the full
// captured vector. The source's probe-fact revision remains a separate value.
func prepareBasicProjectedProbe(source basicStreamFactStage, changeRevision int64, capture basicAssetProjection, after basicProbeScalars) (basicProbeEffects, error) {
	if source.changeID <= 0 || source.inputRevision <= 0 || source.sourceGeneration <= 0 || source.policyRevision <= 0 || source.factRevision <= 0 || source.detailVersion < 0 || source.witnessFingerprint == ([32]byte{}) || source.fingerprint == "" {
		return basicProbeEffects{}, ErrInvalid
	}
	o := capture.observation
	if source.assetID != o.assetID || source.libraryID != o.libraryID || source.size != o.size || source.modifiedNS != o.modifiedNS ||
		source.assetToken != capture.assetToken || source.expectedEpoch != capture.expectedEpoch || source.runID != o.runID || source.sourceID != o.sourceID || source.incarnation != o.incarnation || source.objectID != o.objectID || source.objectRevision != o.objectRevision {
		return basicProbeEffects{}, ErrConflict
	}
	remaining := maxCanonicalRequestBytes
	for _, s := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint, o.assetPath,
		capture.before.container, capture.before.videoCodec, capture.before.audioCodec, after.container, after.videoCodec, after.audioCodec} {
		if len(s) > remaining {
			return basicProbeEffects{}, ErrCap
		}
		remaining -= len(s)
	}
	for _, s := range [...]string{source.assetToken, source.runID, source.sourceID, source.incarnation, source.objectID, source.objectRevision, source.fingerprint} {
		if s == "" || !utf8.ValidString(s) {
			return basicProbeEffects{}, ErrInvalid
		}
	}
	in := basicProbeEffects{source: source, changeRevision: changeRevision, assetPath: o.assetPath, before: capture.before, after: after,
		playbackIncarnation: capture.playbackIncarnation, playbackRevision: capture.playbackRevision, preparedRevision: capture.preparedRevision, fileSizeRevision: capture.fileSizeRevision, invalidationRevision: capture.invalidationRevision}
	if err := validateBasicProbeEffects(in); err != nil {
		return basicProbeEffects{}, err
	}
	in.source = cloneBasicSource(source)
	in.assetPath = strings.Clone(in.assetPath)
	in.before = basicProbeScalars{strings.Clone(in.before.container), strings.Clone(in.before.videoCodec), strings.Clone(in.before.audioCodec), in.before.width, in.before.height, in.before.duration}
	in.after = basicProbeScalars{strings.Clone(after.container), strings.Clone(after.videoCodec), strings.Clone(after.audioCodec), after.width, after.height, after.duration}
	return in, nil
}
