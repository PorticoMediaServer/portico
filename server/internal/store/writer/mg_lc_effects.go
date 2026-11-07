package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private effects ONLY, sharing the existing commandTurn. The accepting frame
// owns fresh authority/lifetimes, original receipt replay, complete admission,
// terminal rollback/COMMIT resolution and final disclosure. Any error after a
// first write requires WHOLE-command rollback. No writer/SQL handle is exposed.
func mgLCStep(turn *commandTurn, id stmt.ID) (statementStep, error) {
	if turn == nil {
		return statementStep{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return statementStep{}, ErrInvalid
	}
	return step.statementStep, nil
}
func mgLCRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, err := mgLCStep(turn, id)
	if err != nil {
		return commandRow{}, err
	}
	return turn.queryRow(ctx, step, args...)
}
func mgLCOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, err := mgLCStep(turn, id)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}
func mgLCNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Sample the SAME writer clock/high-water and protected lifetime as its frame.
// A lease is a logical build fence only. It never releases a playback pin,
// decoder, file holder or physical grant, even after this build has published.
func mgLCTime(ctx context.Context, turn *commandTurn, until int64) (int64, error) {
	if turn == nil || turn.transaction == nil {
		return 0, ErrInvalid
	}
	tx := turn.transaction
	if tx.writer == nil || tx.writer.now == nil || tx.ctx == nil || until <= 0 {
		return 0, ErrInvalid
	}
	if err := turn.checkLifetimes(ctx); err != nil {
		return 0, err
	}
	now := tx.writer.now()
	if err := tx.observeTime(now); err != nil {
		return 0, err
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok || now.UnixMilli() < 0 {
		return 0, ErrInvalid
	}
	if !now.Before(time.UnixMilli(until)) || deadline.After(time.UnixMilli(until)) {
		return 0, ErrConflict
	}
	if !now.Before(deadline) {
		return 0, context.DeadlineExceeded
	}
	return now.UnixMilli(), nil
}

func observeMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild) (int64, error) {
	if !b.valid() {
		return 0, ErrInvalid
	}
	now, err := mgLCTime(ctx, turn, b.leaseUntil)
	if err != nil {
		return 0, err
	}
	row, err := mgLCRow(ctx, turn, mgLCHead, b.channel, b.revision, b.config, b.base)
	if err != nil {
		return 0, err
	}
	var revision int64
	var base string
	if err := row.Scan(&revision, &base); err != nil {
		return 0, mgLCNoRows(err)
	}
	if revision != b.revision || base != b.base {
		return 0, ErrConflict
	}
	for _, w := range b.libraries {
		row, err := mgLCRow(ctx, turn, mgLCLibrary, w.id, w.catalogue)
		if err != nil {
			return 0, err
		}
		var revision, order, metadata, origin int64
		var incarnation []byte
		if err := row.Scan(&revision, &order, &metadata, &origin, &incarnation); err != nil {
			return 0, mgLCNoRows(err)
		}
		if revision != w.revision || order != w.order || metadata != w.metadata || origin != w.origin || !bytes.Equal(incarnation, w.incarnation[:]) {
			return 0, ErrConflict
		}
	}
	row, err = mgLCRow(ctx, turn, mgLCHistory)
	if err != nil {
		return 0, err
	}
	var history int64
	if err := row.Scan(&history); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	// The generation keeps ONE immutable history cutoff. Later accepted plays
	// cannot reorder its candidate pages; the native header also compares that
	// exact cutoff. A cutoff beyond retained history is never accepted. The
	// parent must pin this read coverage against concurrent history retirement.
	if history < b.history {
		return 0, ErrConflict
	}
	return now, nil
}
func readMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild) (mgLCProgress, error) {
	if _, err := observeMGLC(ctx, turn, b); err != nil {
		return mgLCProgress{}, err
	}
	row, err := mgLCRow(ctx, turn, mgLCRead, b.values()...)
	if err != nil {
		return mgLCProgress{}, err
	}
	var p mgLCProgress
	if err := row.Scan(&p.phase, &p.cursor, &p.candidates, &p.unresolved, &p.after); err != nil {
		return mgLCProgress{}, mgLCNoRows(err)
	}
	if p.cursor < b.start || p.cursor > b.end || p.candidates < 0 || p.unresolved < 0 {
		return mgLCProgress{}, ErrConflict
	}
	return p, nil
}
func advanceMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, before, after mgLCProgress) (mgLCProgress, error) {
	if _, err := mgLCTime(ctx, turn, b.leaseUntil); err != nil {
		return mgLCProgress{}, err
	}
	args := append([]any{after.phase, after.cursor, after.candidates, after.unresolved, after.after}, b.values()...)
	args = append(args, before.phase, before.cursor, before.candidates, before.unresolved, before.after)
	row, err := mgLCRow(ctx, turn, mgLCAdvance, args...)
	if err != nil {
		return mgLCProgress{}, err
	}
	var got mgLCProgress
	if err := row.Scan(&got.phase, &got.cursor, &got.candidates, &got.unresolved, &got.after); err != nil {
		return mgLCProgress{}, mgLCNoRows(err)
	}
	if got != after {
		return mgLCProgress{}, ErrConflict
	}
	return got, nil
}
func beginMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	now, err := observeMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	args := append(b.values(), b.start, b.start, now)
	if err := mgLCOne(ctx, turn, mgLCInsert, args...); err != nil {
		return out, err
	}
	return readMGLC(ctx, turn, b)
}

// Stages at most eight admitted read candidates, including exact rule-local
// ordinals and the producer's scan cursor (which includes rejected candidates).
// No UPSERT/DO NOTHING: duplicate bodies must resolve their original receipt.
func stageMGLCCandidates(ctx context.Context, turn *commandTurn, b mgLCBuild, before mgLCProgress, rows []mgLCCandidate, next string, scanned, unresolved int64) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if len(rows) > mgLCRows || scanned <= 0 || scanned > mgLCRows || unresolved < 0 || unresolved > scanned-int64(len(rows)) || !mgLCText(next) || next == before.after {
		return out, ErrInvalid
	}
	bytes := len(next)
	for _, c := range rows {
		for _, s := range []string{c.rule, c.library, c.asset, c.source, c.title, c.sort} {
			if len(s) > mgLCBytes-bytes {
				return out, ErrCap
			}
			bytes += len(s)
		}
		if bytes > mgLCBytes-64 {
			return out, ErrCap
		}
		bytes += 64
		if !c.valid(b) {
			return out, ErrInvalid
		}
	}
	if bytes > mgLCBytes || before.candidates > math.MaxInt64-int64(len(rows)) || before.unresolved > math.MaxInt64-unresolved {
		return out, ErrCap
	}
	current, err := readMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	if current != before || current.phase != mgLCCandidatesPhase || current.cursor != b.start {
		return out, ErrConflict
	}
	for _, c := range rows {
		row, err := mgLCRow(ctx, turn, mgLCTail, b.generation, c.rule)
		if err != nil {
			return out, err
		}
		last := int64(-1)
		if err := row.Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if last == math.MaxInt64 || c.ordinal != last+1 {
			return out, ErrConflict
		}
		if err := mgLCOne(ctx, turn, mgLCCandidateInsert, b.generation, c.rule, c.item, c.ordinal, c.library, c.asset, c.source, c.duration, c.title, c.show, c.season, c.episode, c.weight, c.sort); err != nil {
			return out, err
		}
	}
	after := before
	after.candidates += int64(len(rows))
	after.unresolved += unresolved
	after.after = next
	return advanceMGLC(ctx, turn, b, before, after)
}

// The parent may call this only after the immutable RB/RS producer reaches its
// exact EOF for all rules. This is a phase effect, not a durable manifest seal.
func finishMGLCCandidates(ctx context.Context, turn *commandTurn, b mgLCBuild, before mgLCProgress, total int64) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	current, err := readMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	if current != before || current.phase != mgLCCandidatesPhase || total < 0 || current.candidates != total || current.cursor != b.start {
		return out, ErrConflict
	}
	after := before
	after.phase = mgLCSchedulePhase
	return advanceMGLC(ctx, turn, b, before, after)
}

func stageMGLCEntries(ctx context.Context, turn *commandTurn, b mgLCBuild, before mgLCProgress, slots []mgLCSlot) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if len(slots) == 0 || len(slots) > mgLCRows {
		return out, ErrInvalid
	}
	cursor := before.cursor
	bytes := 0
	for _, s := range slots {
		if s.occurrence == "" || s.rule == "" || s.block == "" || !mgLCText(s.occurrence) || !mgLCText(s.rule) || !mgLCText(s.block) || s.item < 0 || s.start != cursor || s.end <= s.start || s.end > b.end || s.item == 0 && s.end-s.start > 30*60*1000 {
			return out, ErrInvalid
		}
		bytes += len(s.occurrence) + len(s.rule) + len(s.block) + 64
		cursor = s.end
	}
	if bytes > mgLCBytes {
		return out, ErrCap
	}
	current, err := readMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	if current != before || current.phase != mgLCSchedulePhase {
		return out, ErrConflict
	}
	for _, s := range slots {
		if s.item != 0 {
			row, err := mgLCRow(ctx, turn, mgLCCandidateRead, b.generation, s.rule, s.item)
			if err != nil {
				return out, err
			}
			var duration int64
			if err := row.Scan(&duration); err != nil {
				return out, mgLCNoRows(err)
			}
			if s.end-s.start > duration {
				return out, ErrConflict
			}
		}
		if err := mgLCOne(ctx, turn, mgLCEntryInsert, b.generation, s.occurrence, b.channel, s.start, s.end, s.block, b.generation, s.rule, s.item, b.generation, s.occurrence, b.channel, s.start, s.end, s.rule, s.block, s.item); err != nil {
			return out, err
		}
	}
	after := before
	after.cursor = cursor
	if cursor == b.end {
		after.phase = mgLCCompletePhase
	}
	return advanceMGLC(ctx, turn, b, before, after)
}

type mgLCPublication struct {
	generation                                 string
	through, candidates, unresolved, published int64
}

func publishMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, before mgLCProgress) (out mgLCPublication, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	current, err := readMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	if current != before || current.phase != mgLCCompletePhase || current.cursor != b.end {
		return out, ErrConflict
	}
	now, err := mgLCTime(ctx, turn, b.leaseUntil)
	if err != nil {
		return out, err
	}
	state, health := "healthy", ""
	if current.candidates == 0 {
		state = "no-eligible-media"
		if current.unresolved > 0 {
			state = "unresolved-duration"
		}
		health = state
	}
	row, err := mgLCRow(ctx, turn, mgLCHeadPublish, b.generation, current.cursor, state, health, b.channel, b.revision, b.config, b.base)
	if err != nil {
		return out, err
	}
	if err := row.Scan(&out.generation, &out.through); err != nil {
		return mgLCPublication{}, mgLCNoRows(err)
	}
	if out.generation != b.generation || out.through != current.cursor {
		return mgLCPublication{}, ErrConflict
	}
	args := append([]any{now}, b.values()...)
	args = append(args, current.candidates, current.unresolved)
	row, err = mgLCRow(ctx, turn, mgLCGenerationPublish, args...)
	if err != nil {
		return mgLCPublication{}, err
	}
	var through int64
	if err := row.Scan(&through, &out.candidates, &out.unresolved, &out.published); err != nil {
		return mgLCPublication{}, mgLCNoRows(err)
	}
	if through != out.through || out.candidates != current.candidates || out.unresolved != current.unresolved || out.published != now {
		return mgLCPublication{}, ErrConflict
	}
	// Values describe native effects, not Applied. No receipt, event, pin release,
	// old-generation deletion or physical retirement is implied by this result.
	return out, nil
}
