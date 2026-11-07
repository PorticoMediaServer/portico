package writer

import (
	"bytes"
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"
)

// Complete admitted demuxer tuple. Empty audio/container and zero dimensions
// are values, not missing observations. No filename fallback is applied here.
type basicProbeScalars struct {
	container, videoCodec, audioCodec string
	width, height                     int64
	duration                          float64
}
type basicProbeEffects struct {
	source                                                                     basicStreamFactStage
	changeRevision                                                             int64
	assetPath                                                                  string
	before, after                                                              basicProbeScalars
	playbackIncarnation                                                        [16]byte
	playbackRevision, preparedRevision, fileSizeRevision, invalidationRevision int64
}
type basicProbeProgress struct{ changedFields, playbackRevision, preparedRevision int64 }

// applyBasicProbeScalars captures OLD and writes the coherent raw NEW tuple in
// ONE stage transaction. It does not activate info_epoch, replace canonical
// children, choose an owner, clear pending, or suppress family publication when
// its own six scalar values happen to be unchanged.
func applyBasicProbeScalars(ctx context.Context, turn *commandTurn, in basicProbeEffects) (value basicProbeProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	s := in.source
	if ctx == nil || turn == nil {
		return value, ErrInvalid
	}
	if err = validateBasicProbeEffects(in); err != nil {
		return value, err
	}
	if err = checkBasicStreamInput(ctx, turn, s); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicProbeHeader, s.changeID, s.assetID, s.assetToken, int64(0), int64(511), s.inputRevision, in.changeRevision, int64(0), int64(0), int64(0), int64(0), int64(0))
	if err != nil {
		return value, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.changeID {
		return value, ErrConflict
	}
	beforeArgs := basicProbeBeforeArgs(in)
	row, err = basicStageRead(ctx, turn, basicProbeAsset, beforeArgs...)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.assetID {
		return value, ErrConflict
	}
	if in.before == in.after {
		return basicProbeProgress{0, in.playbackRevision, in.preparedRevision}, nil
	}
	// FIRST owns a newly allocated immutable input. Previously captured OLD is
	// retained, never overwritten or borrowed by a new fresh scalar operation.
	old, err := basicRows(ctx, turn, basicProbeOldPresence, s.changeID, int64(1))
	if err != nil {
		return value, err
	}
	defer func() { err = errors.Join(err, old.Close()) }()
	if old.Next() {
		return value, ErrConflict
	}
	if err = old.Err(); err != nil {
		return value, err
	}
	if err = old.Close(); err != nil {
		return value, err
	}
	var changed [6]bool
	changed[0], changed[1], changed[2] = in.before.container != in.after.container, in.before.videoCodec != in.after.videoCodec, in.before.audioCodec != in.after.audioCodec
	changed[3], changed[4], changed[5] = in.before.width != in.after.width, in.before.height != in.after.height, in.before.duration != in.after.duration
	charge := int64(0)
	for n, yes := range changed {
		if yes {
			value.changedFields++
			charge += 176
			if n < 3 {
				charge += int64(len([3]string{in.before.container, in.before.videoCodec, in.before.audioCodec}[n]))
			}
		}
	}
	origin, nextPrepared := in.playbackRevision, in.preparedRevision
	if changed[0] {
		if origin == math.MaxInt64 || nextPrepared == math.MaxInt64 {
			return value, ErrCap
		}
		origin++
		nextPrepared++
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
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != used+charge {
		return value, ErrConflict
	}
	for n, yes := range changed {
		if !yes {
			continue
		}
		var integer, real, text any
		kind := int64(3)
		switch n {
		case 0, 1, 2:
			text = [3]string{in.before.container, in.before.videoCodec, in.before.audioCodec}[n]
		case 3:
			kind = 1
			integer = in.before.width
		case 4:
			kind = 1
			integer = in.before.height
		case 5:
			kind = 2
			real = in.before.duration
		}
		args := []any{s.changeID, int64(n + 4), kind, integer, real, text}
		rows, e := basicRows(ctx, turn, basicProbeOldInsert, args...)
		if e != nil {
			return value, e
		}
		if e = basicMediaExactRow(rows, args); e != nil {
			return value, e
		}
	}
	args := []any{in.after.container, in.after.videoCodec, in.after.audioCodec, in.after.width, in.after.height, in.after.duration, origin, nextPrepared}
	args = append(args, beforeArgs...)
	row, err = basicStageRead(ctx, turn, basicProbeAssetCAS, args...)
	if err != nil {
		return value, err
	}
	var after basicProbeScalars
	var actualSize, actualInvalidation, epoch int64
	var incarnation []byte
	if err = row.Scan(&actual, &after.container, &after.videoCodec, &after.audioCodec, &after.width, &after.height, &after.duration, &value.playbackRevision, &value.preparedRevision, &actualSize, &actualInvalidation, &epoch, &incarnation); err != nil {
		return value, basicStageError(err)
	}
	if actual != s.assetID || after != in.after || value.playbackRevision != origin || value.preparedRevision != nextPrepared || actualSize != in.fileSizeRevision || actualInvalidation != in.invalidationRevision || epoch != s.expectedEpoch || !bytes.Equal(incarnation, in.playbackIncarnation[:]) {
		return value, ErrConflict
	}
	return value, nil
}

func validateBasicProbeEffects(in basicProbeEffects) error {
	if in.changeRevision <= 0 || in.playbackRevision <= 0 || in.preparedRevision <= 0 || in.fileSizeRevision <= 0 || in.invalidationRevision <= 0 || in.playbackIncarnation == ([16]byte{}) {
		return ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	for _, v := range [...]string{in.assetPath, in.before.container, in.before.videoCodec, in.before.audioCodec, in.after.container, in.after.videoCodec, in.after.audioCodec} {
		if len(v) > remaining {
			return ErrCap
		}
		remaining -= len(v)
	}
	if strings.IndexByte(in.assetPath, 0) >= 0 {
		return ErrInvalid
	}
	if !utf8.ValidString(in.assetPath) {
		return ErrFilenameUnrepresentable
	}
	for _, v := range [...]string{in.assetPath, in.before.container, in.before.videoCodec, in.before.audioCodec, in.after.container, in.after.videoCodec, in.after.audioCodec} {
		if !utf8.ValidString(v) {
			return ErrInvalid
		}
	}
	if in.assetPath == "" || strings.IndexByte(in.assetPath, 0) >= 0 || in.after.videoCodec == "" || in.after.width < 0 || in.after.height < 0 || in.after.duration <= 0 || math.IsNaN(in.after.duration) || math.IsInf(in.after.duration, 0) || math.IsNaN(in.before.duration) || math.IsInf(in.before.duration, 0) {
		return ErrInvalid
	}

	return nil
}

func basicProbeBeforeArgs(in basicProbeEffects) []any {
	s := in.source
	return []any{s.assetID, s.assetToken, s.libraryID, in.assetPath, s.size, s.modifiedNS, int64(1), in.before.container, in.before.videoCodec, in.before.audioCodec, in.before.width, in.before.height, in.before.duration, in.playbackIncarnation[:], in.playbackRevision, in.preparedRevision, in.fileSizeRevision, in.invalidationRevision, s.expectedEpoch, int64(0)}
}
