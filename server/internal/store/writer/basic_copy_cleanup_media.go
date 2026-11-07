package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// cleanupBasicMedia returns only the actual native continuation. The caller
// MUST persist it in this SAME turn before committing. Exhaustion is not full
// publication completion. Family3 walks every core key, including absent breadth;
// family5 owns chapter children only, never their fact parent or staged rows.
func cleanupBasicMedia(ctx context.Context, turn *commandTurn, in basicStreamCopyFence) (value basicStreamCleanupResult, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	f, p := in.family, in.position
	if ctx == nil || turn == nil || turn.transaction == nil || f == nil || !f.valid || (f.family != 3 && f.family != 5) ||
		p.changeID != f.source.changeID || p.changeRevision <= 0 || p.phase != 2 || p.afterEntity <= 0 ||
		p.family != f.family || p.copyPhase != 1 || p.id < 0 || p.aux < 0 || p.aux > 1 || (p.aux == 0 && p.id != 0) ||
		p.key != "" || p.key2 != "" || p.key3 != "" || in.shapeID <= 0 || in.fileSizeRevision <= 0 || in.invalidation <= 0 {
		return value, ErrInvalid
	}
	if int64(len(f.rows)) > basicMediaFamilyLimit(f.family) {
		return value, ErrCap
	}
	if f.family == 5 {
		if f.rawChapterCount < 0 || int64(len(f.rows)) > f.rawChapterCount {
			return value, ErrInvalid
		}
		if f.rawChapterCount > MaxRawChapters {
			return value, ErrCap
		}
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
	var first, after, retained, mutate stmt.ID
	switch f.family {
	case 3:
		first, after, retained, mutate = basicCleanupBreadthFirst, basicCleanupBreadthAfter, basicCleanupBreadthRetained, basicCleanupBreadthClear
	case 5:
		first, after, retained, mutate = basicCleanupChapterFirst, basicCleanupChapterAfter, basicCleanupChapterRetained, basicCleanupChapterDelete
	default:
		return value, ErrInvalid
	}
	id := first
	args := []any{s.assetToken}
	if p.aux == 1 {
		id = after
		args = append(args, p.id)
	}
	rows, e := basicRows(ctx, turn, id, args...)
	if e != nil {
		return value, e
	}
	var keys [9]int64
	var present [9]int64
	n := 0
	for rows.Next() {
		if n == len(keys) {
			_ = rows.Close()
			return value, ErrConflict
		}
		if f.family == 3 {
			e = rows.Scan(&keys[n], &present[n])
		} else {
			e = rows.Scan(&keys[n])
		}
		if e != nil {
			_ = rows.Close()
			return value, e
		}
		if (f.family == 3 && present[n] != 0 && present[n] != 1) || keys[n] < 0 || (n > 0 && keys[n] <= keys[n-1]) || (p.aux == 1 && keys[n] <= p.id) {
			_ = rows.Close()
			return value, ErrConflict
		}
		n++
	}
	if e = errors.Join(rows.Err(), rows.Close()); e != nil {
		return value, e
	}
	result := basicStreamCleanupResult{id: p.id, hasAfter: p.aux == 1, more: n == 9}
	for offset, key := range keys[:min(n, 8)] {
		row, e := basicStageRead(ctx, turn, retained, s.changeID, s.assetToken, key)
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
			if f.family == 3 && present[offset] == 0 {
				result.id, result.hasAfter = key, true
				continue
			}
			row, e = basicStageRead(ctx, turn, mutate, s.assetToken, key)
			if e != nil {
				return value, e
			}
			var cleared basicStreamBreadth
			var has int64
			if f.family == 3 {
				e = row.Scan(&token, &index, &has, &cleared.audioDescription, &cleared.audioVariant, &cleared.chroma, &cleared.fpsNumerator, &cleared.fpsDenominator, &cleared.frameMode, &cleared.interlace, &cleared.telecine, &cleared.doviRPU, &cleared.doviBase, &cleared.doviKnown, &cleared.doviEnhancement, &cleared.doviCM, &cleared.playbackFramesRevision, &cleared.packetPeakRevision)
			} else {
				e = row.Scan(&token, &index)
			}
			if e != nil {
				return value, basicStageError(e)
			}
			if token != s.assetToken || index != key || has != 0 || cleared != (basicStreamBreadth{}) {
				return value, ErrConflict
			}
		} else {
			return value, e
		}
		result.id, result.hasAfter = key, true
	}
	return result, nil
}
