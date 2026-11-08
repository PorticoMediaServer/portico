package writer

import (
	"context"
	"database/sql"
	"math"
	"unicode/utf8"
)

// Only this retryable fault may be cleared. Integrity/unsupported faults remain
// hidden and require their own recovery decision. No per-input claim is minted:
// the enclosing Basic command's current run lease is the authority.
const basicResumeRetryFault = "retry"
const basicResumeMaxAttempts int64 = 4294967295

func validateBasicResumeRequest(in basicControlRequest, purpose BasicPurpose) error {
	if purpose != BasicPurposeStage || in.probe != nil || in.abandon != nil || in.family != 0 || in.rawChapters != 0 || in.rows != nil || in.start != 0 || in.end != 0 ||
		in.changeRevision != 0 || in.entityID != 0 || in.shapeID != 0 || in.copy != (basicCopyCursor{}) {
		return ErrInvalid
	}
	return nil
}

// A same-transaction, read-only snapshot of the frozen FIRST header. It is
// transient input to the existing copy bodies, never a saved result/wire field.
// Byte lengths are proved before fetching any native cursor/fault TEXT.
type basicResumeSnapshot struct {
	phase, revision, entity, upper, hasAfter, shape int64
	cursor                                          basicCopyCursor
	owner, generation, expires, attempts            int64
	next                                            sql.NullInt64
	fault                                           string
	lengths                                         [4]int64
}

func (h basicResumeSnapshot) eligible(now int64) bool {
	if h.revision <= 0 || h.revision == math.MaxInt64 || h.entity <= 0 || h.entity != h.upper || h.hasAfter != 1 || h.shape <= 0 ||
		h.attempts < 0 || h.attempts >= basicResumeMaxAttempts || (h.next.Valid && (h.next.Int64 < 0 || h.next.Int64 > now)) ||
		(h.fault != "" && h.fault != basicResumeRetryFault) {
		return false
	}
	// All-zero means unclaimed. A partially populated lease is not an expired
	// claim; refuse it rather than silently converting malformed ownership.
	if !(h.owner == 0 && h.generation == 0 && h.expires == 0) &&
		!(h.owner > 0 && h.generation > 0 && h.expires > 0 && h.expires <= now) {
		return false
	}
	c := h.cursor
	if h.phase == 3 {
		return c == (basicCopyCursor{})
	}
	if h.phase != 2 {
		return false
	}
	if c == (basicCopyCursor{}) {
		return true
	} // selected, before the first copy
	return c.family >= 2 && c.family <= 9 && c.phase >= 0 && c.id >= 0 && c.aux >= 0 && c.aux <= 1
}

func readBasicResumeSnapshot(ctx context.Context, turn *commandTurn, in basicControlRequest) (value basicResumeSnapshot, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	s := in.source
	row, err := basicStageRead(ctx, turn, basicResumeHeader, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:])
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.phase, &value.revision, &value.entity, &value.upper, &value.hasAfter, &value.shape,
		&value.cursor.family, &value.cursor.phase, &value.cursor.id, &value.cursor.aux, &value.owner, &value.generation, &value.expires, &value.attempts, &value.next); err != nil {
		return value, basicStageError(err)
	}
	row, err = basicStageRead(ctx, turn, basicResumeLengths, s.changeID, value.revision)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.lengths[0], &value.lengths[1], &value.lengths[2], &value.lengths[3]); err != nil {
		return value, basicStageError(err)
	}
	required, err := basicControlRequiredBytes(in)
	if err != nil {
		return value, err
	}
	remaining := int64(maxCanonicalRequestBytes)
	for _, n := range value.lengths {
		if n < 0 || n > remaining {
			return value, ErrCap
		}
		remaining -= n
		required += 64 * uint64(n)
	}
	if err = turn.requireBasicMemory(ctx, BasicPurposeStage, required); err != nil {
		return value, err
	}
	row, err = basicStageRead(ctx, turn, basicResumeTexts, s.changeID, value.revision)
	if err != nil {
		return value, err
	}
	if err = row.Scan(&value.cursor.key, &value.cursor.key2, &value.cursor.key3, &value.fault); err != nil {
		return value, basicStageError(err)
	}
	for i, text := range []string{value.cursor.key, value.cursor.key2, value.cursor.key3, value.fault} {
		if int64(len(text)) != value.lengths[i] || !utf8.ValidString(text) {
			return value, ErrConflict
		}
	}
	// Both publication obligations must still belong to this hidden input.
	row, err = basicStageRead(ctx, turn, basicResumeAsset, s.assetID, s.libraryID, s.expectedEpoch)
	if err != nil {
		return value, err
	}
	var pending int64
	if err = row.Scan(&pending); err != nil {
		return value, basicStageError(err)
	}
	if pending != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicResumeLink, value.entity, s.assetID, s.libraryID)
	if err != nil {
		return value, err
	}
	var link sql.NullInt64
	if err = row.Scan(&link); err != nil {
		return value, basicStageError(err)
	}
	if !link.Valid || link.Int64 != s.changeID {
		return value, ErrConflict
	}
	return value, nil
}

// ONE saved-cursor turn, never an unbounded drain. On a nonzero copy cursor the
// producer submits another original; ErrConflict requires a new snapshot/run
// original. An unresolved original is resolved/retried unchanged via the normal
// receipt path before any reset or copy can run again.
func resumeBasicCopy(ctx context.Context, turn *commandTurn, in basicControlRequest, now int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	h, err := readBasicResumeSnapshot(ctx, turn, in)
	if err != nil {
		return err
	}
	if !h.eligible(now) {
		return ErrConflict
	}
	// Phase 2's existing terminal copy CAS advances job_revision. A completed
	// phase-3 resume has no copy body and advances it here instead.
	nextRevision := h.revision
	if h.phase == 3 {
		nextRevision++
	}
	s := in.source
	args := []any{nil, nextRevision, s.changeID, s.assetID, s.assetToken, s.expectedEpoch, s.inputRevision, s.witnessFingerprint[:], h.revision, h.phase, h.shape, h.entity, h.upper, h.hasAfter}
	args = append(args, h.cursor.args()...)
	args = append(args, h.owner, h.generation, h.expires, h.attempts)
	reset := basicResumeReset
	if h.next.Valid {
		reset = basicResumeResetDue
		args = append(args, h.next.Int64)
	}
	args = append(args, h.fault)
	row, err := basicStageRead(ctx, turn, reset, args...)
	if err != nil {
		return err
	}
	var revision, attempts int64
	if err = row.Scan(&revision, &attempts); err != nil {
		return basicStageError(err)
	}
	if revision != nextRevision || attempts != h.attempts+1 {
		return ErrConflict
	}
	if h.phase == 2 {
		copy := in
		copy.operation, copy.family, copy.changeRevision, copy.entityID, copy.shapeID, copy.copy = basicControlCopyFamily, h.cursor.family, h.revision, h.entity, h.shape, h.cursor
		if copy.family == 0 {
			copy.family = 1
		}
		if err = copyBasicFamily(ctx, turn, copy); err != nil {
			return err
		}
		nextRevision++
	} else if err = checkBasicInputAtPhase(ctx, turn, s, 3); err != nil {
		return err
	}
	// Retry bookkeeping is retained while hidden. At successful copy completion
	// it returns to the idle tuple required by the existing final retirement;
	// neither publication fence is touched, and only Accept may expose the asset.
	row, err = basicStageRead(ctx, turn, basicResumePhase, s.changeID, nextRevision)
	if err != nil {
		return err
	}
	var phase int64
	if err = row.Scan(&phase); err != nil {
		return basicStageError(err)
	}
	if phase == 2 {
		return nil
	}
	if phase != 3 {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicResumeFinish, s.changeID, nextRevision, attempts)
	if err != nil {
		return err
	}
	var gotAttempts int64
	if err = row.Scan(&phase, &revision, &gotAttempts); err != nil {
		return basicStageError(err)
	}
	if phase != 3 || revision != nextRevision || gotAttempts != 0 {
		return ErrConflict
	}
	return nil
}
