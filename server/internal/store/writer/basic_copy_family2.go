package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Family 2 (asset_streams). The schema-v1 target keeps each stream's breadth
// columns on the SAME canonical row, so a stream row is the family's whole
// canonical identity: no separate breadth child can survive a deleted parent.
//
//   - phase 0 = INSTALL: at most 8 staged ordinals per turn (input_ordinal
//     keyset), inserted or replaced by an exact old-value CAS; the RETURNING row
//     must equal the staged row.
//   - phase 1 = CLEANUP: canonical (asset_id,stream_index) keys in index order,
//     at most 8 per turn; a key the staged set does not name is deleted after
//     RETURNING proves the exact row.
//   - ONE cursor CAS per turn; the phase-0 completion checkpoint is the phase-1
//     start cursor {phase:1,id:0,aux:0} and the family exit is {family:3}.
const (
	basicCopy2Install = 8 // ordinals installed per phase-0 turn
	basicCopy2Collect = 10
	basicCopy2Walk    = 8 // canonical keys processed per phase-1 turn
	basicCopy2Keys    = 9
)

type basicCopy2Row struct {
	ordinal int64
	row     basicStreamRow
}

func basicCopy2Entry(family int64, entry basicCopyCursor) bool {
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

func copyBasicFamily2(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !basicCopy2Entry(2, c.entry) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	if c.entry.phase == 0 {
		return basicCopy2InstallStage(ctx, turn, c)
	}
	return basicCopy2Cleanup(ctx, turn, c)
}

// basicCopy2Read collects the staged keyset. The checkpointed ordinal names a
// row the previous turn already installed, so the read starts at it (>=) and
// the body skips that one row instead of losing ordinal 0 at the first turn.
func basicCopy2Read(ctx context.Context, turn *commandTurn, c basicCopyTurn) (out []basicCopy2Row, err error) {
	s := c.source
	rows, err := basicRows(ctx, turn, basicCopy2Staged, s.changeID, s.assetToken, c.entry.id)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if len(out) == basicCopy2Collect {
			return nil, ErrConflict
		}
		var token string
		var value basicCopy2Row
		var def, forced int64
		if err = rows.Scan(&value.ordinal, &token, &value.row.index, &value.row.streamType, &value.row.codec, &value.row.language,
			&value.row.title, &value.row.channels, &value.row.channelLayout, &def, &forced, &value.row.colorTransfer,
			&value.row.colorPrimaries, &value.row.detail); err != nil {
			return nil, err
		}
		if token != s.assetToken || value.ordinal < c.entry.id || (def != 0 && def != 1) || (forced != 0 && forced != 1) {
			return nil, ErrConflict
		}
		value.row.isDefault, value.row.isForced = def == 1, forced == 1
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func basicCopy2InstallStage(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	staged, err := basicCopy2Read(ctx, turn, c)
	if err != nil {
		return err
	}
	start := 0
	if c.entry.id > 0 && len(staged) > 0 && staged[0].ordinal == c.entry.id {
		start = 1 // the checkpoint named this already-installed row
	}
	pending := staged[start:]
	more := len(pending) > basicCopy2Install
	if more {
		pending = pending[:basicCopy2Install]
	}
	last := c.entry.id
	for _, value := range pending {
		if err = basicCopy2InstallRow(ctx, turn, c, value); err != nil {
			return err
		}
		last = value.ordinal
	}
	if more {
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 2, phase: 0, id: last})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 2, phase: 1})
}

func basicCopy2InstallRow(ctx context.Context, turn *commandTurn, c basicCopyTurn, staged basicCopy2Row) error {
	s, r := c.source, staged.row
	row, err := basicStageRead(ctx, turn, basicCopy2Canonical, s.assetToken, r.index)
	if err != nil {
		return err
	}
	var old basicStreamRow
	var def, forced int64
	err = row.Scan(&old.index, &old.streamType, &old.codec, &old.language, &old.title, &old.channels, &old.channelLayout,
		&def, &forced, &old.colorTransfer, &old.colorPrimaries, &old.detail)
	hasCanonical := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	old.isDefault, old.isForced = def == 1, forced == 1
	if hasCanonical {
		row, err = basicStageRead(ctx, turn, basicCopy2Update, basicCopy2WriteArgs(r, s.assetToken, old)...)
	} else {
		row, err = basicStageRead(ctx, turn, basicCopy2Insert, basicCopy2InsertArgs(r, s.assetToken)...)
	}
	if err != nil {
		return err
	}
	var got basicCopy2Row
	var token string
	var gotDef, gotForced int64
	if err = row.Scan(&token, &got.row.index, &got.row.streamType, &got.row.codec, &got.row.language, &got.row.title,
		&got.row.channels, &got.row.channelLayout, &gotDef, &gotForced, &got.row.colorTransfer, &got.row.colorPrimaries,
		&got.row.detail); err != nil {
		return basicStageError(err)
	}
	got.row.isDefault, got.row.isForced = gotDef == 1, gotForced == 1
	if token != s.assetToken || got.row != r {
		return ErrConflict
	}
	return nil
}

func basicCopy2InsertArgs(r basicStreamRow, token string) []any {
	return []any{token, r.index, r.streamType, r.codec, r.language, r.title, r.channels, r.channelLayout,
		basicBool(r.isDefault), basicBool(r.isForced), r.colorTransfer, r.colorPrimaries, r.detail}
}

// The new values, the exact key and every canonical value read before the
// update. A concurrent canonical rewrite of any column makes the CAS miss.
func basicCopy2WriteArgs(r basicStreamRow, token string, old basicStreamRow) []any {
	args := []any{r.streamType, r.codec, r.language, r.title, r.channels, r.channelLayout,
		basicBool(r.isDefault), basicBool(r.isForced), r.colorTransfer, r.colorPrimaries, r.detail, token, r.index}
	return append(args, old.streamType, old.codec, old.language, old.title, old.channels, old.channelLayout,
		basicBool(old.isDefault), basicBool(old.isForced), old.colorTransfer, old.colorPrimaries, old.detail)
}

func basicCopy2Cleanup(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	s := c.source
	id := basicCopy2WalkFirst
	args := []any{s.assetToken}
	if c.entry.aux == 1 {
		id, args = basicCopy2WalkAfter, append(args, c.entry.id)
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
		if len(keys) == basicCopy2Keys || key < 0 || (len(keys) > 0 && key <= keys[len(keys)-1]) || (c.entry.aux == 1 && key <= c.entry.id) {
			_ = rows.Close()
			return ErrConflict
		}
		keys = append(keys, key)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	batch := keys
	more := len(batch) > basicCopy2Walk
	if more {
		batch = batch[:basicCopy2Walk]
	}
	last := c.entry.id
	for _, key := range batch {
		probe, err := basicStageRead(ctx, turn, basicCopy2Retained, s.changeID, s.assetToken, key)
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
			gone, err := basicStageRead(ctx, turn, basicCopy2Delete, s.assetToken, key)
			if err != nil {
				return err
			}
			var deletedToken string
			var deletedIndex int64
			if err = gone.Scan(&deletedToken, &deletedIndex); err != nil {
				return basicStageError(err)
			}
			if deletedToken != s.assetToken || deletedIndex != key {
				return ErrConflict
			}
		} else {
			return basicStageError(err)
		}
		last = key
	}
	if more {
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 2, phase: 1, id: last, aux: 1})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 3})
}
