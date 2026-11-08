package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Family 5 (asset_chapters). Same bounded shape as family 2:
//
//   - phase 0 = INSTALL: at most 8 staged ordinals per turn, each row requiring
//     its canonical asset_chapter_facts parent (family 4 installed it first);
//     inserts or old-value CAS replacements must RETURN exactly the staged row.
//   - phase 1 = CLEANUP: canonical chapter keys in index order, at most 8 per
//     turn; a key the staged set does not name is deleted by exact RETURNING.
//   - ONE cursor CAS per turn; the phase-0 completion checkpoint is the phase-1
//     start cursor {phase:1,id:0,aux:0} and the family exit is {family:6}.
const (
	basicCopy5Install = 8
	basicCopy5Collect = 10
	basicCopy5Walk    = 8
	basicCopy5Keys    = 9
)

type basicCopy5Row struct {
	ordinal int64
	row     basicChapter
}

func basicCopy5Entry(family int64, entry basicCopyCursor) bool {
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

func copyBasicFamily5(ctx context.Context, turn *commandTurn, c basicCopyTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !basicCopy5Entry(5, c.entry) {
		return ErrInvalid
	}
	if err = checkBasicCopyEntry(ctx, turn, c); err != nil {
		return err
	}
	if c.entry.phase == 0 {
		return basicCopy5InstallStage(ctx, turn, c)
	}
	return basicCopy5Cleanup(ctx, turn, c)
}

func basicCopy5Read(ctx context.Context, turn *commandTurn, c basicCopyTurn) (out []basicCopy5Row, err error) {
	s := c.source
	rows, err := basicRows(ctx, turn, basicCopy5Staged, s.changeID, s.assetToken, c.entry.id)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		if len(out) == basicCopy5Collect {
			return nil, ErrConflict
		}
		var value basicCopy5Row
		var token string
		if err = rows.Scan(&value.ordinal, &token, &value.row.index, &value.row.title, &value.row.start, &value.row.end); err != nil {
			return nil, err
		}
		if token != s.assetToken || value.row.index < 0 || value.ordinal < c.entry.id {
			return nil, ErrConflict
		}
		out = append(out, value)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func basicCopy5InstallStage(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	staged, err := basicCopy5Read(ctx, turn, c)
	if err != nil {
		return err
	}
	start := 0
	if c.entry.id > 0 && len(staged) > 0 && staged[0].ordinal == c.entry.id {
		start = 1
	}
	pending := staged[start:]
	more := len(pending) > basicCopy5Install
	if more {
		pending = pending[:basicCopy5Install]
	}
	last := c.entry.id
	if len(pending) > 0 {
		// The chapter fact is the legacy install guard: no chapter row without
		// its per-asset fact parent.
		row, err := basicStageRead(ctx, turn, basicCopy5Parent, c.source.assetToken)
		if err != nil {
			return err
		}
		var parent string
		if err = row.Scan(&parent); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrConflict
			}
			return basicStageError(err)
		}
		if parent != c.source.assetToken {
			return ErrConflict
		}
	}
	for _, value := range pending {
		if err = basicCopy5InstallRow(ctx, turn, c, value); err != nil {
			return err
		}
		last = value.ordinal
	}
	if more {
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 5, phase: 0, id: last})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 5, phase: 1})
}

func basicCopy5InstallRow(ctx context.Context, turn *commandTurn, c basicCopyTurn, staged basicCopy5Row) error {
	s, r := c.source, staged.row
	row, err := basicStageRead(ctx, turn, basicCopy5Canonical, s.assetToken, r.index)
	if err != nil {
		return err
	}
	var old basicChapter
	err = row.Scan(&old.index, &old.title, &old.start, &old.end)
	hasCanonical := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return basicStageError(err)
	}
	if hasCanonical {
		row, err = basicStageRead(ctx, turn, basicCopy5Update, r.title, r.start, r.end, s.assetToken, r.index,
			old.title, old.start, old.end)
	} else {
		row, err = basicStageRead(ctx, turn, basicCopy5Insert, s.assetToken, r.index, r.title, r.start, r.end)
	}
	if err != nil {
		return err
	}
	var got basicChapter
	var token string
	if err = row.Scan(&token, &got.index, &got.title, &got.start, &got.end); err != nil {
		return basicStageError(err)
	}
	if token != s.assetToken || got != r {
		return ErrConflict
	}
	return nil
}

func basicCopy5Cleanup(ctx context.Context, turn *commandTurn, c basicCopyTurn) error {
	s := c.source
	id := basicCopy5WalkFirst
	args := []any{s.assetToken}
	if c.entry.aux == 1 {
		id, args = basicCopy5WalkAfter, append(args, c.entry.id)
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
		if len(keys) == basicCopy5Keys || key < 0 || (len(keys) > 0 && key <= keys[len(keys)-1]) || (c.entry.aux == 1 && key <= c.entry.id) {
			_ = rows.Close()
			return ErrConflict
		}
		keys = append(keys, key)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	batch := keys
	more := len(batch) > basicCopy5Walk
	if more {
		batch = batch[:basicCopy5Walk]
	}
	last := c.entry.id
	for _, key := range batch {
		probe, err := basicStageRead(ctx, turn, basicCopy5Retained, s.changeID, s.assetToken, key)
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
			gone, err := basicStageRead(ctx, turn, basicCopy5Delete, s.assetToken, key)
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
		return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 5, phase: 1, id: last, aux: 1})
	}
	return advanceBasicCopyCursor(ctx, turn, c, 2, basicCopyCursor{family: 6})
}
