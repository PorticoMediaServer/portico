package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Family 3 (asset_streams breadth columns). Schema-v1 folds the legacy
// asset_stream_breadth table into asset_streams, so this family owns only the
// has_asset_stream_breadth/breadth_* columns of a stream row the family-2 turn
// already installed. Install therefore requires the (asset_id,stream_index)
// parent row and refuses without it; cleanup clears breadth a staged set no
// longer names instead of deleting the family-2 core row.
const (
	basicCopy3Install = 8
	basicCopy3Collect = 10
	basicCopy3Walk    = 8
	basicCopy3Keys    = 9
)

type basicCopy3Row struct {
	ordinal int64
	row     basicStreamBreadth
}

func basicCopy3Entry(family int64, entry basicCopyCursor) bool {
	if entry.family != family || entry.key != "" || entry.key2 != "" || entry.key3 != "" || entry.id < 0 {
		return false
	}
	switch entry.phase {
	case 0:
		return entry.aux == 0
	case 1:
		return (entry.id == 0 && entry.aux == 0) || (entry.id > 0 && entry.aux == 1)
	}
	return false
}

func copyBasicFamily3(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !basicCopy3Entry(3, c.entry) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	if c.entry.phase == 0 {
		return basicCopy3InstallStage(ctx, turn, c)
	}
	return basicCopy3Cleanup(ctx, turn, c)
}

func basicCopy3Read(ctx context.Context, turn *commandTurn, c basicCopyTurn) (out []basicCopy3Row, err error) {
	s := c.source
	rows, err := basicRows(ctx, turn, basicCopy3Staged, s.changeID, s.assetToken, c.entry.id)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if len(out) == basicCopy3Collect {
			return nil, ErrConflict
		}
		var value basicCopy3Row
		var token string
		var ad, telecine, rpu, base, known int64
		if err = rows.Scan(&value.ordinal, &token, &value.row.index, &ad, &value.row.audioVariant, &value.row.chroma,
			&value.row.fpsNumerator, &value.row.fpsDenominator, &value.row.frameMode, &value.row.interlace, &telecine,
			&rpu, &base, &known, &value.row.doviEnhancement, &value.row.doviCM, &value.row.playbackFramesRevision,
			&value.row.packetPeakRevision); err != nil {
			return nil, err
		}
		if token != s.assetToken || value.row.index < 0 || value.ordinal < c.entry.id ||
			(ad != 0 && ad != 1) || (telecine != 0 && telecine != 1) || (rpu != 0 && rpu != 1) || (base != 0 && base != 1) || (known != 0 && known != 1) {
			return nil, ErrConflict
		}
		value.row.audioDescription, value.row.telecine, value.row.doviRPU, value.row.doviBase, value.row.doviKnown = ad == 1, telecine == 1, rpu == 1, base == 1, known == 1
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func basicCopy3InstallStage(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	staged, err := basicCopy3Read(ctx, turn, c)
	if err != nil {
		return err
	}
	start := 0
	if c.entry.id > 0 && len(staged) > 0 && staged[0].ordinal == c.entry.id {
		start = 1
	}
	pending := staged[start:]
	more := len(pending) > basicCopy3Install
	if more {
		pending = pending[:basicCopy3Install]
	}
	last := c.entry.id
	for _, value := range pending {
		if err = basicCopy3InstallRow(ctx, turn, c, value); err != nil {
			return err
		}
		last = value.ordinal
	}
	if more {
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 3, phase: 0, id: last})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 3, phase: 1})
}

// basicCopy3InstallRow installs breadth onto an existing stream row. A missing
// parent is a change conflict, never an implicit family-2 insert.
func basicCopy3InstallRow(ctx context.Context, turn *commandTurn, c basicCopyTurn, staged basicCopy3Row) error {
	s, r := c.source, staged.row
	row, err := basicStageRead(ctx, turn, basicCopy3Canonical, s.assetToken, r.index)
	if err != nil {
		return err
	}
	var old basicStreamBreadth
	var has int64
	var ad, telecine, rpu, base, known int64
	err = row.Scan(&has, &ad, &old.audioVariant, &old.chroma, &old.fpsNumerator, &old.fpsDenominator, &old.frameMode,
		&old.interlace, &telecine, &rpu, &base, &known, &old.doviEnhancement, &old.doviCM, &old.playbackFramesRevision,
		&old.packetPeakRevision)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict // no family-2 parent stream row for this staged breadth
	}
	if err != nil {
		return basicStageError(err)
	}
	old.index = r.index
	if (ad != 0 && ad != 1) || (telecine != 0 && telecine != 1) || (rpu != 0 && rpu != 1) || (base != 0 && base != 1) ||
		(known != 0 && known != 1) || (has != 0 && has != 1) {
		return ErrConflict
	}
	old.audioDescription, old.telecine, old.doviRPU, old.doviBase, old.doviKnown = ad == 1, telecine == 1, rpu == 1, base == 1, known == 1
	row, err = basicStageRead(ctx, turn, basicCopy3Update, basicCopy3WriteArgs(r, s.assetToken, has, old)...)
	if err != nil {
		return err
	}
	var got basicStreamBreadth
	var token string
	var gotHas int64
	if err = row.Scan(&token, &got.index, &gotHas, &got.audioDescription, &got.audioVariant, &got.chroma, &got.fpsNumerator,
		&got.fpsDenominator, &got.frameMode, &got.interlace, &got.telecine, &got.doviRPU, &got.doviBase, &got.doviKnown,
		&got.doviEnhancement, &got.doviCM, &got.playbackFramesRevision, &got.packetPeakRevision); err != nil {
		return basicStageError(err)
	}
	if token != s.assetToken || gotHas != 1 || got != r {
		return ErrConflict
	}
	return nil
}

func basicCopy3ValueArgs(value basicStreamBreadth) []any {
	return []any{basicBool(value.audioDescription), value.audioVariant, value.chroma, value.fpsNumerator, value.fpsDenominator,
		value.frameMode, value.interlace, basicBool(value.telecine), basicBool(value.doviRPU), basicBool(value.doviBase),
		basicBool(value.doviKnown), value.doviEnhancement, value.doviCM, value.playbackFramesRevision, value.packetPeakRevision}
}

// The new breadth, the exact key, then the presence flag and every canonical
// breadth value read before the update (the strongest CAS the folded row has).
func basicCopy3WriteArgs(value basicStreamBreadth, token string, has int64, old basicStreamBreadth) []any {
	args := basicCopy3ValueArgs(value)
	args = append(args, token, value.index, has)
	return append(args, basicCopy3ValueArgs(old)...)
}

func basicCopy3Cleanup(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	s := c.source
	id := basicCopy3WalkFirst
	args := []any{s.assetToken}
	if c.entry.aux == 1 {
		id, args = basicCopy3WalkAfter, append(args, c.entry.id)
	}
	rows, err := basicRows(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var keys []int64
	for rows.Next() {
		var key int64
		if err = rows.Scan(&key); err != nil {
			_ = rows.Close()
			return err
		}
		if len(keys) == basicCopy3Keys || key < 0 || (len(keys) > 0 && key <= keys[len(keys)-1]) || (c.entry.aux == 1 && key <= c.entry.id) {
			_ = rows.Close()
			return ErrConflict
		}
		keys = append(keys, key)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	batch := keys
	more := len(batch) > basicCopy3Walk
	if more {
		batch = batch[:basicCopy3Walk]
	}
	last := c.entry.id
	for _, key := range batch {
		probe, err := basicStageRead(ctx, turn, basicCopy3Retained, s.changeID, s.assetToken, key)
		if err != nil {
			return err
		}
		var change int64
		var token string
		var index int64
		err = probe.Scan(&change, &token, &index)
		if err == nil {
			if change != s.changeID || token != s.assetToken || index != key {
				return ErrConflict
			}
		} else if errors.Is(err, sql.ErrNoRows) {
			cleared, err := basicStageRead(ctx, turn, basicCopy3Clear, s.assetToken, key)
			if err != nil {
				return err
			}
			var clearedToken string
			var clearedIndex, has int64
			var value basicStreamBreadth
			if err = cleared.Scan(&clearedToken, &clearedIndex, &has, &value.audioDescription, &value.audioVariant,
				&value.chroma, &value.fpsNumerator, &value.fpsDenominator, &value.frameMode, &value.interlace,
				&value.telecine, &value.doviRPU, &value.doviBase, &value.doviKnown, &value.doviEnhancement, &value.doviCM,
				&value.playbackFramesRevision, &value.packetPeakRevision); err != nil {
				return basicStageError(err)
			}
			value.index = clearedIndex
			if clearedToken != s.assetToken || clearedIndex != key || has != 0 || value != (basicStreamBreadth{index: key}) {
				return ErrConflict
			}
		} else {
			return basicStageError(err)
		}
		last = key
	}
	if more {
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 3, phase: 1, id: last, aux: 1})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 4})
}
