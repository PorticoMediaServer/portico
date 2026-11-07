package writer

import "context"

// A closed domain router, called only after the same Stage turn's receipt-first
// lookup, whole memory check, native fresh vector and trusted time sample.
// It grants no frame, resource, current authority or publication Outcome.
func applyBasicCopy(ctx context.Context, turn *commandTurn, in basicCopyRequest, appliedMS int64) (value basicCopyPosition, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	if err = validateBasicCopyRequest(in); err != nil {
		return value, err
	}
	if in.operation == basicCopyPrefence {
		pending, e := acceptBasicOrdinaryInput(ctx, turn, in.prefence, appliedMS)
		if e != nil {
			return value, e
		}
		original := in.prefence
		original.stream.changeRevision = pending.changeRevision
		original.snapshot.changeRevision = pending.changeRevision
		return prefenceBasicOrdinary(ctx, turn, basicPrefenceEffects{input: original})
	}
	if in.operation == basicCopyStreamFact {
		return copyBasicStreamFactStep(ctx, turn, basicStreamCopyEffects{source: in.source, position: in.position, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation, hadBefore: in.hadBefore, before: in.beforeStreamFact})
	}
	family, err := basicCopyPreparedFamily(in)
	if err != nil {
		return value, err
	}
	fence := basicStreamCopyFence{family: family, position: in.position, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation, ordinal: in.startOrdinal}
	switch in.operation {
	case basicCopyMediaRow:
		var before basicMediaRow
		switch in.rows.family {
		case 2:
			before = in.beforeStreamRow
		case 3:
			before = in.beforeBreadth
		case 5:
			before = in.beforeChapter
		case 7:
			before = in.beforeSidecar
		default:
			return value, ErrInvalid
		}
		return copyBasicMediaRowStep(ctx, turn, basicMediaCopyStep{fence: fence, hadBefore: in.hadBefore, before: before})
	case basicCopyChapterFact:
		return copyBasicChapterFactStep(ctx, turn, basicChapterFactCopyEffects{family: family, position: in.position, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation, ordinal: in.startOrdinal, hadBefore: in.hadBefore, before: in.beforeChapterFact})
	case basicCopySubtitleFact:
		return copyBasicSubtitleFactStep(ctx, turn, basicSubtitleFactCopyEffects{family: family, position: in.position, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation, ordinal: in.startOrdinal, hadBefore: in.hadBefore, before: in.beforeSubtitleFact})
	case basicCopyEvidence:
		return copyBasicSubtitleEvidenceStep(ctx, turn, basicSubtitleEvidenceCopyEffects{family: family, position: in.position, shapeID: in.shapeID, fileSizeRevision: in.fileSizeRevision, invalidation: in.invalidation, ordinal: in.startOrdinal, hadBefore: in.hadBefore, before: in.beforeEvidence})
	case basicCopyEmptyFamily:
		return startBasicEmptyFamily(ctx, turn, fence)
	case basicCopyCleanupStreams:
		return cleanupBasicStreamStep(ctx, turn, fence)
	case basicCopyCleanupMedia:
		return cleanupBasicMediaStep(ctx, turn, fence)
	case basicCopySubtitleRow:
		return copyBasicSubtitleStep(ctx, turn, basicSubtitleCopyEffects{basicStreamCopyFence: fence, hadBefore: in.hadBefore, before: in.beforeSubtitle})
	default:
		return value, ErrInvalid
	}
}

func basicCopyPreparedFamily(in basicCopyRequest) (*basicMediaFamily, error) {
	var chunk [8]basicMediaRow
	n := basicRowsCount(in.rows)
	if n > int64(len(chunk)) {
		return nil, ErrCap
	}
	// The request constructor has frozen exactly one typed field. Interfaces
	// exist only in this charged derivation; no interface is hashed or retained
	// in the original command request.
	for i := int64(0); i < n; i++ {
		switch in.rows.family {
		case 2:
			chunk[i] = in.rows.streams[i]
		case 3:
			chunk[i] = in.rows.breadth[i]
		case 4:
			chunk[i] = in.rows.chapterFacts[i]
		case 5:
			chunk[i] = in.rows.chapters[i]
		case 6:
			chunk[i] = in.rows.subtitleFacts[i]
		case 7:
			chunk[i] = in.rows.sidecars[i]
		case 8:
			chunk[i] = in.rows.subtitles[i]
		case 9:
			chunk[i] = in.rows.evidence[i]
		default:
			return nil, ErrInvalid
		}
	}
	return prepareBasicCopyFamily(in.source, in.rows.family, in.expectedRows, in.startOrdinal, in.rawChapterCount, chunk[:n])
}
func basicCopyContinuation(p basicCopyPosition) BasicContinuation {
	return BasicContinuation{NextFamily: p.family, NextCopyPhase: p.copyPhase, NextCopyID: p.id, NextCopyAux: p.aux, NextCopyKey: p.key, NextCopyKey2: p.key2, NextCopyKey3: p.key3, AfterEntity: p.afterEntity}
}
