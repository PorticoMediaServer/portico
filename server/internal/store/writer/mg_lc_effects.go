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
func scanMGLCProgress(row commandRow) (mgLCProgress, error) {
	var p mgLCProgress
	var digest, verified []byte
	var sealed int64
	err := row.Scan(&p.phase, &p.after, &p.status, &p.lease, &p.reclaimFamily, &p.reclaimKey, &p.cursor, &p.candidates, &p.unresolved, &p.last, &p.ack, &p.revision, &p.bytes, &p.entries, &p.verified, &p.verifyAfter, &p.verifyEnd, &p.leaseUntil, &p.published, &p.reclaimSubkey, &digest, &verified, &sealed)
	if err != nil {
		return p, mgLCNoRows(err)
	}
	if len(digest) != 32 || len(verified) != 32 || (sealed != 0 && sealed != 1) {
		return mgLCProgress{}, ErrConflict
	}
	copy(p.entryDigest[:], digest)
	copy(p.verifyDigest[:], verified)
	p.sealed = sealed == 1
	return p, nil
}
func readMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild) (mgLCProgress, error) {
	if _, err := observeMGLC(ctx, turn, b); err != nil {
		return mgLCProgress{}, err
	}
	row, err := mgLCRow(ctx, turn, mgLCRead, b.values()...)
	if err != nil {
		return mgLCProgress{}, err
	}
	p, err := scanMGLCProgress(row)
	if err != nil {
		return mgLCProgress{}, err
	}
	if p.cursor < b.start || p.cursor > b.end || p.candidates < 0 || p.unresolved < 0 || p.last < 1 || p.ack < 0 || p.ack > p.last || p.last-p.ack > 1 || p.revision < 1 || p.bytes < 0 || p.entries < 0 || p.verified < 0 || p.verified > p.entries || p.verifyAfter < -1 || p.verifyEnd < b.start || p.verifyEnd > p.cursor {
		return mgLCProgress{}, ErrConflict
	}
	if p.verified == 0 && (p.verifyAfter != -1 || p.verifyEnd != b.start || p.verifyDigest != mgLCInitialDigest(b)) {
		return mgLCProgress{}, ErrConflict
	}
	return p, nil
}
func freshMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) error {
	if !c.valid(b) {
		return ErrInvalid
	}
	current, err := readMGLC(ctx, turn, b)
	if err != nil {
		return err
	}
	if current != before || before.last != before.ack || before.last == math.MaxInt64 || before.revision == math.MaxInt64 || c.identity.Sequence != before.last+1 {
		return ErrConflict
	}
	// An existing operation ID at another sequence must never execute fresh.
	row, err := mgLCRow(ctx, turn, mgLCReceiptOperation, b.generation, b.incarnation[:], c.identity.OperationID[:])
	if err != nil {
		return err
	}
	var sequence int64
	if err := row.Scan(&sequence); !errors.Is(err, sql.ErrNoRows) {
		if err != nil {
			return err
		}
		return ErrConflict
	}
	return nil
}
func advanceMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before, after mgLCProgress) (mgLCProgress, error) {
	if _, err := mgLCTime(ctx, turn, b.leaseUntil); err != nil {
		return mgLCProgress{}, err
	}
	after.last = c.identity.Sequence
	after.revision = before.revision + 1
	args := append(after.values(), b.values()...)
	args = append(args, before.values()...)
	row, err := mgLCRow(ctx, turn, mgLCAdvance, args...)
	if err != nil {
		return mgLCProgress{}, err
	}
	got, err := scanMGLCProgress(row)
	if err != nil {
		return mgLCProgress{}, err
	}
	if got != after {
		return mgLCProgress{}, ErrConflict
	}
	return got, nil
}
func beginMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if !c.valid(b) || c.identity.Sequence != 1 || c.identity.OperationID != b.create || c.request != b.birth {
		return out, ErrInvalid
	}
	if err := excludeMGLCBirth(ctx, turn, b); err != nil {
		return out, err
	}
	now, err := observeMGLC(ctx, turn, b)
	if err != nil {
		return out, err
	}
	digest := mgLCInitialDigest(b)
	args := append(b.values(), b.start, b.start, now, digest[:], b.start, digest[:])
	row, err := mgLCRow(ctx, turn, mgLCInsert, args...)
	if err != nil {
		return out, err
	}
	out, err = scanMGLCProgress(row)
	if err != nil {
		return mgLCProgress{}, err
	}
	expected := mgLCProgress{phase: mgLCCandidatesPhase, status: "pending", lease: b.lease, reclaimFamily: "none", cursor: b.start, last: 1, revision: 1, verifyAfter: -1, verifyEnd: b.start, leaseUntil: b.leaseUntil, entryDigest: digest, verifyDigest: digest}
	if out != expected {
		return mgLCProgress{}, ErrConflict
	}
	return out, nil
}

// At most eight raw read candidates, including filtered/unresolved rows, and
// 256KiB canonical inserted content per effect. Actual RETURNING rows alone
// advance candidate_count/staged_bytes. Duplicates require original receipts.
func stageMGLCCandidates(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress, input []mgLCCandidate, next string, scanned, unresolved int64) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if len(input) > mgLCRows || scanned <= 0 || scanned > mgLCRows || unresolved < 0 || unresolved > scanned-int64(len(input)) || len(next) > mgLCBytes || !mgLCText(next) || next == before.after {
		return out, ErrInvalid
	}
	budget := 0
	for _, v := range input {
		// Lengths are checked before walking/copying owned text.
		size := 113 + len(b.generation)
		for _, s := range []string{v.rule, v.library, v.asset, v.source, v.title, v.sort} {
			if len(s) > mgLCBytes-size {
				return out, ErrCap
			}
			size += len(s)
		}
		if size > mgLCBytes-budget {
			return out, ErrCap
		}
		budget += size
		if !v.valid(b) {
			return out, ErrInvalid
		}
	}
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCCandidatesPhase || before.sealed || before.cursor != b.start || before.candidates > math.MaxInt64-int64(len(input)) || before.unresolved > math.MaxInt64-unresolved || before.bytes > math.MaxInt64-int64(budget) {
		return out, ErrConflict
	}
	after := before
	for _, v := range input {
		row, err := mgLCRow(ctx, turn, mgLCTail, b.generation, v.rule)
		if err != nil {
			return out, err
		}
		last := int64(-1)
		if err := row.Scan(&last); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return out, err
		}
		if last == math.MaxInt64 || v.ordinal != last+1 {
			return out, ErrConflict
		}
		row, err = mgLCRow(ctx, turn, mgLCCandidateInsert, b.generation, v.rule, v.item, v.ordinal, v.library, v.asset, v.source, v.duration, v.title, v.show, v.season, v.episode, v.weight, v.sort)
		if err != nil {
			return out, err
		}
		var actual mgLCCandidate
		var generation string
		if err := row.Scan(&generation, &actual.rule, &actual.item, &actual.ordinal, &actual.library, &actual.asset, &actual.source, &actual.duration, &actual.title, &actual.show, &actual.season, &actual.episode, &actual.weight, &actual.sort); err != nil {
			return out, mgLCNoRows(err)
		}
		if generation != b.generation || actual != v {
			return out, ErrConflict
		}
		after.candidates++
		after.bytes += int64(len(mgLCCandidateBytes(generation, actual)))
	}
	after.unresolved += unresolved
	after.after = next
	return advanceMGLC(ctx, turn, b, c, before, after)
}

// Candidate EOF is the compiled immutable RB/RS producer's exact end, not a
// caller-supplied expected count. No new candidate counter/manifest is invented.
func finishMGLCCandidates(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCCandidatesPhase || before.sealed || before.cursor != b.start {
		return out, ErrConflict
	}
	after := before
	after.phase = mgLCSchedulePhase
	return advanceMGLC(ctx, turn, b, c, before, after)
}
func stageMGLCEntries(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress, slots []mgLCSlot) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if len(slots) == 0 || len(slots) > mgLCRows {
		return out, ErrInvalid
	}
	cursor := before.cursor
	for _, s := range slots {
		if len(s.occurrence)+len(s.rule)+len(s.block) > mgLCBytes {
			return out, ErrCap
		}
		if s.occurrence == "" || s.rule == "" || s.block == "" || !mgLCText(s.occurrence) || !mgLCText(s.rule) || !mgLCText(s.block) || s.item < 0 || s.start != cursor || s.end <= s.start || s.end > b.end || s.item == 0 && s.end-s.start > 30*60*1000 {
			return out, ErrInvalid
		}
		cursor = s.end
	}
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCSchedulePhase || before.sealed || before.entries > math.MaxInt64-int64(len(slots)) {
		return out, ErrConflict
	}
	after := before
	var debit int64
	for _, s := range slots {
		e := mgLCEntry{generation: b.generation, occurrence: s.occurrence, channel: b.channel, start: s.start, end: s.end, item: s.item, rule: s.rule, block: s.block}
		size := int64(113 + len(e.generation) + len(e.occurrence) + len(e.channel) + len(e.rule) + len(e.block))
		if s.item == 0 {
			e.title = "Unavailable program"
			e.slate = "no-eligible-media"
			size += int64(len(e.title) + len(e.slate))
		} else {
			row, err := mgLCRow(ctx, turn, mgLCCandidateRead, b.generation, s.rule, s.item)
			if err != nil {
				return out, err
			}
			var duration, bytes int64
			if err := row.Scan(&duration, &bytes); err != nil {
				return out, mgLCNoRows(err)
			}
			if duration <= 0 || s.end-s.start > duration || bytes < 0 {
				return out, ErrConflict
			}
			if bytes > mgLCBytes-size {
				return out, ErrCap
			}
			size += bytes
			if size > mgLCBytes-debit {
				return out, ErrCap
			}
			row, err = mgLCRow(ctx, turn, mgLCEntrySource, b.generation, s.rule, s.item)
			if err != nil {
				return out, err
			}
			if err := row.Scan(&e.asset, &e.library, &e.source, &e.title); err != nil {
				return out, mgLCNoRows(err)
			}
		}
		if size > mgLCBytes-debit || before.bytes > math.MaxInt64-debit-size {
			return out, ErrCap
		}
		row, err := mgLCRow(ctx, turn, mgLCEntryInsert, e.values()...)
		if err != nil {
			return out, err
		}
		var actual mgLCEntry
		if err := row.Scan(&actual.generation, &actual.occurrence, &actual.channel, &actual.start, &actual.end, &actual.item, &actual.asset, &actual.library, &actual.source, &actual.title, &actual.rule, &actual.block, &actual.offset, &actual.slate); err != nil {
			return out, mgLCNoRows(err)
		}
		if actual != e || int64(len(actual.canonicalBytes())) != size {
			return out, ErrConflict
		}
		debit += size
		after.entries++
		after.entryDigest = mgLCNextDigest(after.entryDigest, actual)
	}
	after.bytes += debit
	after.cursor = cursor
	if cursor == b.end {
		after.phase = mgLCCompletePhase
	}
	return advanceMGLC(ctx, turn, b, c, before, after)
}

// Independent stored-row proof, <=8 raw rows and <=256KiB decoded CONTENT per
// step. A bounded key page precedes exact full-PK octet preflights, all on
// this same command turn, before hydration. A distinct empty-tail step proves
// EOF, so extra rows cannot hide behind an exact-size final page.
func verifyMGLCEntries(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCCompletePhase || before.sealed || before.cursor != b.end {
		return out, ErrConflict
	}
	keyID := mgLCEntryKeys
	args := []any{b.generation, before.verifyAfter}
	if before.verified == 0 {
		keyID = mgLCEntryKeysFirst
		args = []any{b.generation}
	}
	step, err := mgLCStep(turn, keyID)
	if err != nil {
		return out, err
	}
	rows, err := turn.query(ctx, step, args...)
	if err != nil {
		return out, err
	}
	var starts []int64
	var occurrences []string
	for rows.Next() {
		var start int64
		var occurrence string
		if err := rows.Scan(&start, &occurrence); err != nil {
			_ = rows.Close()
			return out, err
		}
		starts = append(starts, start)
		occurrences = append(occurrences, occurrence)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	after := before
	if len(starts) == 0 {
		if before.entries == 0 || before.verified != before.entries || before.verifyEnd != before.cursor || before.verifyDigest != before.entryDigest {
			return out, ErrConflict
		}
		after.phase = mgLCVerifiedPhase
		return advanceMGLC(ctx, turn, b, c, before, after)
	}
	if starts[0] != before.verifyEnd {
		return out, ErrConflict
	}
	count := 0
	var sizes []int64
	var bytes int64
	for _, occurrence := range occurrences {
		point, err := mgLCRow(ctx, turn, mgLCEntryLengthsPoint, b.generation, occurrence)
		if err != nil {
			return out, err
		}
		var size int64
		if err := point.Scan(&size); err != nil {
			return out, mgLCNoRows(err)
		}
		if size < 113 || size > mgLCBytes {
			return out, ErrCap
		}
		if size > mgLCBytes-bytes {
			break
		}
		bytes += size
		sizes = append(sizes, size)
		count++
	}
	if count == 0 || int64(count) > before.entries-before.verified {
		return out, ErrConflict
	}
	step, err = mgLCStep(turn, mgLCEntryPage)
	if err != nil {
		return out, err
	}
	rows, err = turn.query(ctx, step, b.generation, before.verifyAfter, starts[count-1], int64(count))
	if err != nil {
		return out, err
	}
	n := 0
	for rows.Next() {
		var e mgLCEntry
		if err := rows.Scan(&e.generation, &e.occurrence, &e.channel, &e.start, &e.end, &e.item, &e.asset, &e.library, &e.source, &e.title, &e.rule, &e.block, &e.offset, &e.slate); err != nil {
			_ = rows.Close()
			return out, err
		}
		if n >= count || e.generation != b.generation || e.channel != b.channel || e.start != starts[n] || e.occurrence != occurrences[n] || e.start != after.verifyEnd || e.end <= e.start || e.end > b.end || e.offset != 0 || int64(len(e.canonicalBytes())) != sizes[n] {
			_ = rows.Close()
			return out, ErrConflict
		}
		after.verifyAfter = e.start
		after.verifyEnd = e.end
		after.verified++
		after.verifyDigest = mgLCNextDigest(after.verifyDigest, e)
		n++
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	if n != count {
		return out, ErrConflict
	}
	return advanceMGLC(ctx, turn, b, c, before, after)
}
func sealMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCVerifiedPhase || before.sealed || before.entries == 0 || before.verified != before.entries || before.verifyDigest != before.entryDigest || before.verifyEnd != before.cursor || before.cursor != b.end {
		return out, ErrConflict
	}
	after := before
	after.sealed = true
	after.phase = mgLCSealedPhase
	return advanceMGLC(ctx, turn, b, c, before, after)
}
func publishMGLC(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err := freshMGLC(ctx, turn, b, c, before); err != nil {
		return out, err
	}
	if before.phase != mgLCSealedPhase || !before.sealed || before.entries == 0 || before.verified != before.entries || before.verifyDigest != before.entryDigest || before.verifyEnd != before.cursor || before.cursor != b.end {
		return out, ErrConflict
	}
	now, err := mgLCTime(ctx, turn, b.leaseUntil)
	if err != nil {
		return out, err
	}
	state, health := "healthy", ""
	if before.candidates == 0 {
		state = "no-eligible-media"
		if before.unresolved > 0 {
			state = "unresolved-duration"
		}
		health = state
	}
	row, err := mgLCRow(ctx, turn, mgLCHeadPublish, b.generation, before.cursor, state, health, b.channel, b.revision, b.config, b.base)
	if err != nil {
		return out, err
	}
	var generation string
	var through int64
	if err := row.Scan(&generation, &through); err != nil {
		return out, mgLCNoRows(err)
	}
	if generation != b.generation || through != before.cursor {
		return out, ErrConflict
	}
	after := before
	after.phase = "published"
	after.status = "published"
	after.lease = ""
	after.leaseUntil = 0
	after.published = now
	return advanceMGLC(ctx, turn, b, c, before, after)
}
