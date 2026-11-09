package writer

// Accepting frames for the LC producer commands 860-866. MG-21 supplied the
// private native effects and receipts; this file binds them to their closed
// purposes, resolves the ORIGINAL saved result before any effect, and adds the
// producer continuation facts (rule-state/used) and offset/continuation entry
// staging. The frame owns no runtime registration, worker polling, catalogue
// read or physical retirement. Publication is still a single old-head+config
// CAS on the sealed generation; G reclamation stays off.

import (
	"context"
	"errors"
	"math"
	"strconv"
)

// mgLCDispatchSlot is one frozen schedule interval from the lcbuild producer.
// A non-zero offset is a continued source position, not a new asset.
type mgLCDispatchSlot struct {
	occurrence, rule, block  string
	item, start, end, offset int64
}

// mgLCDispatchInput is bounded, owned producer input for one command. A kind
// uses only its own fields; anything else must be empty.
type mgLCDispatchInput struct {
	candidates          []mgLCCandidate
	next                string
	scanned, unresolved int64
	end                 bool
	slots               []mgLCDispatchSlot
	rules               []mgLCRuleState
	used                []mgLCUsed
}

// mgLCDispatchDescriptors exposes exactly the seven accepting producer
// purposes. MG-21's 867 ACK descriptor remains prospective for its own task.
func mgLCDispatchDescriptors() []Descriptor {
	var out []Descriptor
	for _, d := range mgLCDescriptors() {
		if d.ID >= MGLCBegin && d.ID <= MGLCSeal {
			out = append(out, d)
		}
	}
	return out
}

// 860-866 are NORMAL producer/publication purposes. 867 ACK remains owned by
// the receipt/retention task and cannot borrow these frames.
func mgLCDispatchKind(purpose CommandID) (mgLCKind, bool) {
	switch purpose {
	case MGLCBegin:
		return mgLCBeginKind, true
	case MGLCStageCandidates, MGLCFinishCandidates:
		return mgLCCandidatesKind, true
	case MGLCStageEntries:
		return mgLCEntriesKind, true
	case MGLCPublish:
		return mgLCPublishKind, true
	case MGLCVerifyEntries:
		return mgLCVerifyKind, true
	case MGLCSeal:
		return mgLCSealKind, true
	}
	return "", false
}

func mgLCDispatchPurpose(t *commandTurn, purpose CommandID) error {
	if t == nil || t.transaction == nil || t.transaction.descriptor.ID != purpose ||
		t.transaction.descriptor.Durability != Normal || !t.transaction.policyVerified ||
		!t.transaction.begun || t.transaction.done {
		return ErrInvalid
	}
	if _, ok := mgLCDispatchKind(purpose); !ok {
		return ErrInvalid
	}
	return nil
}

func (s mgLCDispatchSlot) valid() bool {
	return s.occurrence != "" && s.rule != "" && s.block != "" && mgLCText(s.occurrence) &&
		mgLCText(s.rule) && mgLCText(s.block) && len(s.occurrence)+len(s.rule)+len(s.block) <= mgLCBytes &&
		s.item >= 0 && s.start >= 0 && s.end > s.start && s.offset >= 0
}

func (in mgLCDispatchInput) valid(kind mgLCKind) error {
	empty := len(in.candidates) == 0 && in.next == "" && in.scanned == 0 && in.unresolved == 0 &&
		!in.end && len(in.slots) == 0 && len(in.rules) == 0 && len(in.used) == 0
	switch kind {
	case mgLCBeginKind, mgLCVerifyKind, mgLCSealKind, mgLCPublishKind:
		if !empty {
			return ErrInvalid
		}
	case mgLCCandidatesKind:
		if len(in.slots) != 0 || len(in.rules) != 0 || len(in.used) != 0 {
			return ErrInvalid
		}
		if in.end {
			if len(in.candidates) != 0 || in.next != "" || in.scanned != 0 || in.unresolved != 0 {
				return ErrInvalid
			}
			return nil
		}
		if len(in.candidates) == 0 || in.scanned <= 0 {
			return ErrInvalid
		}
	case mgLCEntriesKind:
		if len(in.candidates) != 0 || in.next != "" || in.scanned != 0 || in.unresolved != 0 || in.end ||
			len(in.slots) == 0 || len(in.slots) > mgLCRows || len(in.rules) > mgLCRows || len(in.used) > mgLCRows {
			return ErrInvalid
		}
		for _, slot := range in.slots {
			if !slot.valid() {
				return ErrInvalid
			}
		}
	}
	return nil
}

// mgLCDispatch resolves the original receipt FIRST, then performs exactly one
// producer effect on this turn. A saved original is disclosed without a fresh
// head/lease/catalogue check; a retired or ambiguous result is never success.
func mgLCDispatch(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, purpose CommandID, in mgLCDispatchInput) (out mgLCSaved, decision receiptDecision, err error) {
	defer func() {
		value, failure := finishCommand(ctx, commandLifetime(turn), struct {
			Saved    mgLCSaved
			Decision receiptDecision
		}{out, decision}, err)
		out, decision, err = value.Saved, value.Decision, failure
	}()
	if err = mgLCDispatchPurpose(turn, purpose); err != nil {
		return mgLCSaved{}, receiptConflict, err
	}
	kind, ok := mgLCDispatchKind(purpose)
	if !ok || !c.valid(b) || !b.valid() {
		return mgLCSaved{}, receiptConflict, ErrInvalid
	}
	if err = in.valid(kind); err != nil {
		return mgLCSaved{}, receiptConflict, err
	}
	var saved mgLCSaved
	decision, saved, err = lookupMGLCReceipt(ctx, turn, b, c, kind)
	if err != nil {
		return mgLCSaved{}, decision, err
	}
	switch decision {
	case receiptReplay:
		return saved, decision, nil
	case receiptRetired:
		return mgLCSaved{}, decision, ErrDisclosureDenied
	case receiptResolveRequired:
		return mgLCSaved{}, decision, ErrResolveRequired
	case receiptConflict:
		return mgLCSaved{}, decision, ErrConflict
	case receiptFresh:
	default:
		return mgLCSaved{}, receiptConflict, ErrInvalid
	}
	before := mgLCProgress{}
	if kind != mgLCBeginKind {
		before, err = readMGLC(ctx, turn, b)
		if err != nil {
			return mgLCSaved{}, receiptConflict, err
		}
	}
	var progress mgLCProgress
	switch kind {
	case mgLCBeginKind:
		progress, err = beginMGLC(ctx, turn, b, c)
	case mgLCCandidatesKind:
		if in.end {
			progress, err = finishMGLCCandidates(ctx, turn, b, c, before)
		} else {
			progress, err = stageMGLCCandidates(ctx, turn, b, c, before, in.candidates, in.next, in.scanned, in.unresolved)
		}
	case mgLCEntriesKind:
		if err = writeMGLCProducerState(ctx, turn, b, in.rules, in.used); err == nil {
			progress, err = stageMGLCDispatchEntries(ctx, turn, b, c, before, in.slots)
		}
	case mgLCVerifyKind:
		progress, err = verifyMGLCDispatchEntries(ctx, turn, b, c, before)
	case mgLCSealKind:
		progress, err = sealMGLC(ctx, turn, b, c, before)
	case mgLCPublishKind:
		progress, err = publishMGLC(ctx, turn, b, c, before)
	}
	if err != nil {
		return mgLCSaved{}, receiptConflict, err
	}
	saved, err = saveMGLCReceipt(ctx, turn, b, c, kind, progress)
	if err != nil {
		return mgLCSaved{}, receiptConflict, err
	}
	return saved, receiptFresh, nil
}

// The frame always reads its immediate predecessor from the native owner in the
// same turn; a caller-supplied predecessor is never accepted as authority. No
// builder helper here returns a progress value to a caller.

// writeMGLCProducerState persists the producer's bounded rule cursors and used
// marks. Each upsert compares the exact native RETURNING values; a partial
// write or a concurrent continuation change rolls the whole frame back.
func writeMGLCProducerState(ctx context.Context, turn *commandTurn, b mgLCBuild, rules []mgLCRuleState, used []mgLCUsed) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if turn == nil || b.generation == "" || len(rules) > mgLCRows || len(used) > mgLCRows {
		return ErrInvalid
	}
	seenRule := map[string]bool{}
	for _, r := range rules {
		if r.rule == "" || len(r.rule) > 256 || !mgLCText(r.rule) || seenRule[r.rule] || r.draw < 0 ||
			r.cycle < 0 || len(r.lastSort) > mgLCBytes || len(r.lastShow) > 256 || len(r.lastBlock) > 256 ||
			!mgLCText(r.lastSort) || !mgLCText(r.lastShow) || !mgLCText(r.lastBlock) {
			return ErrInvalid
		}
		seenRule[r.rule] = true
		done := int64(0)
		if r.done {
			done = 1
		}
		row, err := mgLCRow(ctx, turn, mgLCRuleStateWrite, b.generation, r.rule, r.draw, r.cycle, r.lastSort, r.lastShow, r.lastBlock, done)
		if err != nil {
			return err
		}
		var draw, cycle, gotDone int64
		var lastSort, lastShow, lastBlock string
		if err := row.Scan(&draw, &cycle, &lastSort, &lastShow, &lastBlock, &gotDone); err != nil {
			return mgLCNoRows(err)
		}
		if draw != r.draw || cycle != r.cycle || lastSort != r.lastSort || lastShow != r.lastShow ||
			lastBlock != r.lastBlock || gotDone != done {
			return ErrConflict
		}
		row, err = mgLCRow(ctx, turn, mgLCRuleStateRead, b.generation, r.rule)
		if err != nil {
			return err
		}
		var stored mgLCRuleState
		var storedDone int64
		if err := row.Scan(&stored.draw, &stored.cycle, &stored.lastSort, &stored.lastShow, &stored.lastBlock, &storedDone); err != nil {
			return mgLCNoRows(err)
		}
		if stored.draw != r.draw || stored.cycle != r.cycle || stored.lastSort != r.lastSort ||
			stored.lastShow != r.lastShow || stored.lastBlock != r.lastBlock || storedDone != done {
			return ErrConflict
		}
	}
	seenUsed := map[string]bool{}
	for _, u := range used {
		key := u.rule + "\x00" + strconv.FormatInt(u.item, 10)
		if u.rule == "" || len(u.rule) > 256 || !mgLCText(u.rule) || u.item <= 0 || u.cycle < 0 || seenUsed[key] {
			return ErrInvalid
		}
		seenUsed[key] = true
		row, err := mgLCRow(ctx, turn, mgLCUsedWrite, b.generation, u.rule, u.item, u.cycle)
		if err != nil {
			return err
		}
		var rule string
		var item, cycle int64
		if err := row.Scan(&rule, &item, &cycle); err != nil {
			return mgLCNoRows(err)
		}
		if rule != u.rule || item != u.item || cycle != u.cycle {
			return ErrConflict
		}
	}
	return nil
}

// stageMGLCDispatchEntries is the offset-capable form of the merged entry
// effect. Zero offsets are byte-identical in accounting; a media slot may start
// at its copied source offset, and every stored RETURNING value is compared.
func stageMGLCDispatchEntries(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress, slots []mgLCDispatchSlot) (out mgLCProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if len(slots) == 0 || len(slots) > mgLCRows {
		return out, ErrInvalid
	}
	cursor := before.cursor
	for _, s := range slots {
		if len(s.occurrence)+len(s.rule)+len(s.block) > mgLCBytes || !s.valid() || s.start != cursor ||
			s.end > b.end || s.item == 0 && s.offset != 0 || s.item == 0 && s.end-s.start > 30*60*1000 {
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
		e := mgLCEntry{generation: b.generation, occurrence: s.occurrence, channel: b.channel, start: s.start, end: s.end, item: s.item, rule: s.rule, block: s.block, offset: s.offset}
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
			var duration int64
			var lengths [4]int64
			if err := row.Scan(&duration, &lengths[0], &lengths[1], &lengths[2], &lengths[3]); err != nil {
				return out, mgLCNoRows(err)
			}
			if duration <= 0 || s.offset >= duration || s.end-s.start > duration-s.offset {
				return out, ErrConflict
			}
			size, err = mgLCOctetBytes(size, lengths[:])
			if err != nil {
				return out, err
			}
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
		if actual != e || int64(len(actual.canonicalBytes())) != size || actual.offset != s.offset {
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

// verifyMGLCDispatchEntries independently re-reads the stored schedule in
// bounded key/octet/page steps. It accepts a genuine continuation offset while
// still refusing a hole, a replaced interval, a foreign generation or a
// fabricated tally.
func verifyMGLCDispatchEntries(ctx context.Context, turn *commandTurn, b mgLCBuild, c mgLCCommand, before mgLCProgress) (out mgLCProgress, err error) {
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
		var lengths [10]int64
		if err := point.Scan(&lengths[0], &lengths[1], &lengths[2], &lengths[3], &lengths[4], &lengths[5], &lengths[6], &lengths[7], &lengths[8], &lengths[9]); err != nil {
			return out, mgLCNoRows(err)
		}
		size, err := mgLCOctetBytes(113, lengths[:])
		if err != nil {
			return out, err
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
	var entries []mgLCEntry
	for rows.Next() {
		var e mgLCEntry
		if err := rows.Scan(&e.generation, &e.occurrence, &e.channel, &e.start, &e.end, &e.item, &e.asset, &e.library, &e.source, &e.title, &e.rule, &e.block, &e.offset, &e.slate); err != nil {
			_ = rows.Close()
			return out, err
		}
		entries = append(entries, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	if len(entries) != count {
		return out, ErrConflict
	}
	for n, e := range entries {
		if e.generation != b.generation || e.channel != b.channel || e.start != starts[n] ||
			e.occurrence != occurrences[n] || e.start != after.verifyEnd || e.end <= e.start || e.end > b.end ||
			int64(len(e.canonicalBytes())) != sizes[n] {
			return out, ErrConflict
		}
		if e.item == 0 {
			if e.offset != 0 || e.slate == "" {
				return out, ErrConflict
			}
		} else if err := mgLCDispatchOffset(ctx, turn, b, e); err != nil {
			return out, err
		}
		after.verifyAfter = e.start
		after.verifyEnd = e.end
		after.verified++
		after.verifyDigest = mgLCNextDigest(after.verifyDigest, e)
	}
	return advanceMGLC(ctx, turn, b, c, before, after)
}

// A continued media entry must still name a stored candidate and start within
// that candidate's whole duration. This is a native fact check, not playback or
// physical-source authority.
func mgLCDispatchOffset(ctx context.Context, turn *commandTurn, b mgLCBuild, e mgLCEntry) error {
	row, err := mgLCRow(ctx, turn, mgLCCandidateRead, b.generation, e.rule, e.item)
	if err != nil {
		return err
	}
	var duration int64
	var lengths [4]int64
	if err := row.Scan(&duration, &lengths[0], &lengths[1], &lengths[2], &lengths[3]); err != nil {
		return mgLCNoRows(err)
	}
	if duration <= 0 || e.offset < 0 || e.offset >= duration || e.end-e.start > duration-e.offset {
		return ErrConflict
	}
	return nil
}
