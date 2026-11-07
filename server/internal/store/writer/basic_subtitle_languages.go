package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This is one canonical track's actual membership delta, not a library/user
// fanout. Empty language is a real native bucket. The parent first proves and
// changes that track, then calls this helper in the SAME receipt transaction.
type basicSubtitleLanguageDelta struct {
	hadBefore, hasAfter bool
	before, after       string
}

func applyBasicSubtitleLanguageDelta(ctx context.Context, turn *commandTurn, in basicSubtitleLanguageDelta) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil || (!in.hadBefore && !in.hasAfter) || (!in.hadBefore && in.before != "") || (!in.hasAfter && in.after != "") {
		return ErrInvalid
	}
	remaining := maxCanonicalRequestBytes
	for _, s := range [...]string{in.before, in.after} {
		if len(s) > remaining {
			return ErrCap
		}
		remaining -= len(s)
	}
	if !utf8.ValidString(in.before) || !utf8.ValidString(in.after) {
		return ErrInvalid
	}
	if in.hadBefore && in.hasAfter && in.before == in.after {
		return turn.checkLifetimes(ctx)
	}
	if in.hadBefore {
		count, e := readBasicSubtitleLanguageCount(ctx, turn, in.before)
		if e != nil {
			if errors.Is(e, sql.ErrNoRows) {
				return ErrConflict
			}
			return e
		}
		if count <= 0 {
			return ErrConflict
		}
		id := basicSubtitleLanguageCAS
		args := []any{count - 1, in.before, count}
		expected := count - 1
		if count == 1 {
			id = basicSubtitleLanguageDelete
			args = []any{in.before, count}
			expected = count
		}
		if e = writeBasicSubtitleLanguageCount(ctx, turn, id, args, in.before, expected); e != nil {
			return e
		}
	}
	if in.hasAfter {
		count, e := readBasicSubtitleLanguageCount(ctx, turn, in.after)
		if errors.Is(e, sql.ErrNoRows) {
			return writeBasicSubtitleLanguageCount(ctx, turn, basicSubtitleLanguageInsert, []any{in.after, int64(1)}, in.after, 1)
		}
		if e != nil {
			return e
		}
		if count <= 0 {
			return ErrConflict
		}
		if count == math.MaxInt64 {
			return ErrCap
		}
		return writeBasicSubtitleLanguageCount(ctx, turn, basicSubtitleLanguageCAS, []any{count + 1, in.after, count}, in.after, count+1)
	}
	return nil
}
func readBasicSubtitleLanguageCount(ctx context.Context, turn *commandTurn, language string) (int64, error) {
	row, err := basicStageRead(ctx, turn, basicSubtitleLanguageRead, language)
	if err != nil {
		return 0, err
	}
	var count int64
	if err = row.Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}
func writeBasicSubtitleLanguageCount(ctx context.Context, turn *commandTurn, id stmt.ID, args []any, language string, count int64) error {
	row, err := basicStageRead(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var actual string
	var tracks int64
	if err = row.Scan(&actual, &tracks); err != nil {
		return basicStageError(err)
	}
	if actual != language || tracks != count {
		return ErrConflict
	}
	return nil
}
