package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"
)

// The caller MUST persist this continuation in the SAME turn before commit.
// This private effect never publishes availability or changes command state.
type basicStreamCleanupResult struct {
	id             int64
	hasAfter, more bool
}

// cleanupBasicStreams examines at most eight native keys, with one native
// lookahead. id is a stream_index, never an input ordinal. aux distinguishes
// the first page from after-index-zero. The schema's only incoming stream FK
// is the folded breadth self-FK; deletion owns that same physical row only.
func cleanupBasicStreams(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) (value basicStreamCleanupResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if ctx == nil || turn == nil || turn.transaction == nil || f == nil || !f.valid || f.family != 2 ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family != 2 || p.copyPhase != 1 || p.id < 0 || p.aux < 0 || p.aux > 1 || (p.aux == 0 && p.id != 0) ||
		p.key != "" || p.key2 != "" || p.key3 != "" || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	if int64(len(f.rows)) > basicMediaFamilyLimit(2) {
		return value, ErrCap
	}
	s := f.source
	remaining := maxCanonicalRequestBytes
	for _, v := range [...]string{s.assetToken, s.runID, s.sourceID, s.incarnation, s.objectID, s.objectRevision, s.fingerprint} {
		if len(v) > remaining {
			return value, ErrCap
		}
		remaining -= len(v)
		if !utf8.ValidString(v) {
			return value, ErrInvalid
		}
	}
	if err = checkBasicStreamCopyFence(ctx, turn, in); err != nil {
		return value, err
	}
	id := basicCleanupStreamFirst
	args := []any{s.assetToken}
	if p.aux == 1 {
		id = basicCleanupStreamAfter
		args = append(args, p.id)
	}
	rows, e := basicRows(ctx, turn, id, args...)
	if e != nil {
		return value, e
	}
	var keys [9]int64
	n := 0
	for rows.Next() {
		if n == len(keys) {
			_ = rows.Close()
			return value, ErrConflict
		}
		if e = rows.Scan(&keys[n]); e != nil {
			_ = rows.Close()
			return value, e
		}
		if keys[n] < 0 || (n > 0 && keys[n] <= keys[n-1]) || (p.aux == 1 && keys[n] <= p.id) {
			_ = rows.Close()
			return value, ErrConflict
		}
		n++
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return value, e
	}
	result := basicStreamCleanupResult{id: p.id, hasAfter: p.aux == 1, more: n == 9}
	for _, key := range keys[:min(n, 8)] {
		row, e := basicStageRead(ctx, turn, basicCleanupStreamRetained, s.changeID, s.assetToken, key)
		if e != nil {
			return value, e
		}
		var change, index int64
		var token string
		e = row.Scan(&change, &token, &index)
		if e == nil {
			if change != s.changeID || token != s.assetToken || index != key {
				return value, ErrConflict
			}
		} else if errors.Is(e, sql.ErrNoRows) {
			row, e = basicStageRead(ctx, turn, basicCleanupStreamDelete, s.assetToken, key)
			if e != nil {
				return value, e
			}
			if e = row.Scan(&token, &index); e != nil {
				return value, basicStageError(e)
			}
			if token != s.assetToken || index != key {
				return value, ErrConflict
			}
		} else {
			return value, e
		}
		result.id, result.hasAfter = key, true
	}
	return result, nil
}
