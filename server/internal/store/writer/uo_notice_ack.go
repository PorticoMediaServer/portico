package writer

import (
	"context"
	"database/sql"
	"errors"
)

// Sparse notice actions for ONE already-authorized principal and inbox. The
// dispatcher supplies the current principal/scope authority BEFORE receipt
// disclosure and calls this only for a fresh command; it owns the saved
// receipt, quota, original replay classification and physical terminal. Every
// error requires whole-turn rollback and every write here is one of the
// closed statement IDs in uo_notice_ack_statements.go.
//
// Command 622 (uo_acknowledge_notices) carries explicit read/unread/archive/
// unarchive operations over exact notice witnesses; command 623
// (uo_read_all_notices) carries one read-through scalar. Both share this native
// component because an ordered batch of at most eight operations is the same
// closed shape, and order between a scalar and a point action is meaningful.
//
// The reader side is the merged read/uo_notice_state.go rule: an individual
// acknowledgement whose revision has caught up with the notice wins with its
// exact times, an archived-but-unread notice is excluded from a read-through,
// and a producer restatement (a newer notice revision) invalidates stale
// acknowledgement state. resolveUONativeAck is the native values-only form of
// that same rule, kept in this package because the read helper is unexported.
const (
	uoNoticeMaxAckOperations  = 8
	uoNoticeMaxAckTargets     = 200
	uoNoticeMaxPrincipalBytes = 512
)

type uoNoticeAction uint8

const (
	uoNoticeRead      uoNoticeAction = 1
	uoNoticeUnread    uoNoticeAction = 2
	uoNoticeArchive   uoNoticeAction = 3
	uoNoticeUnarchive uoNoticeAction = 4
	// Read-through is the one scalar action: it covers every notice in this
	// inbox up to through (0 means the newest record present now). It never
	// names point targets, so it cannot become a per-recipient loop.
	uoNoticeReadThrough uoNoticeAction = 5
)

// The caller's exact witness for one visible notice: public identifier plus the
// record revision and accepted authority revision it saw. A mismatch is a
// conflict, never a guessed target.
type uoNoticeAckTarget struct {
	id                          string
	revision, authorityRevision int64
}

// One ordered action. Order is meaningful: read-through -> unread differs from
// unread -> read-through, so a batch reserves one ordered ordinal range and
// writes its operations in public order.
type uoNoticeAckOperation struct {
	action  uoNoticeAction
	through int64
	targets []uoNoticeAckTarget
}

type uoNoticeAckInput struct {
	view       uoNoticeView
	principal  string
	nowMS      int64
	operations []uoNoticeAckOperation
}

// The complete outcome of one action. changed means this action wrote native
// acknowledgement state; notFound covers absent, withdrawn and expired notices,
// which are indistinguishable to this principal and never create a row.
type uoNoticeAckOutcome struct {
	action             uoNoticeAction
	id                 string
	through            int64
	changed, notFound  bool
	revision           int64
	readMS, archivedMS sql.NullInt64
}

// One acknowledgement command's result. revision is the last ordinal this
// command reserved; changed counts the actions that actually wrote state.
// Exactly one hint is appended per changed command, never one per notice.
type uoNoticeAckEffect struct {
	revision int64
	changed  int
	outcomes []uoNoticeAckOutcome
	event    wcAPIEventEffect
}

// The current individual acknowledgement facts for one notice and principal.
type uoNoticeAckState struct {
	exists             bool
	revision           int64
	readMS, archivedMS sql.NullInt64
}

// The one read-through head for one principal. sequence is the highest notice
// sequence any read-through has covered and readMS is the accepted time of the
// newest covering action; neither may regress. archive_through_sequence stays
// zero because there is no archive-all action in this contract.
type uoNoticeAckHead struct {
	exists                     bool
	sequence, revision, readMS int64
}

// bindUONoticeAckRoot uses the same named refusal seam as UO-12's
// bindUONoticeRoot. The private lifetime-root receipt projection (UO-01) and
// the shared root dispatcher (UO-03) are separate tasks; this build cannot mint
// a root receipt, payload commitment or Applied result from a native effect, so
// a caller that needs the root-bound result must refuse rather than fabricate
// custody. Remove this refusal only with the actual merged root projection.
func bindUONoticeAckRoot(uoNoticeAckEffect) (uoNoticeRootBinding, error) {
	return uoNoticeRootBinding{}, ErrNoticeRootUnavailable
}

func (q uoNoticeAckInput) validate() error {
	if !q.view.valid() || !uoNoticeText(q.principal, uoNoticeMaxPrincipalBytes, true) || q.nowMS <= 0 ||
		len(q.operations) < 1 || len(q.operations) > uoNoticeMaxAckOperations {
		return ErrInvalid
	}
	targets := 0
	for _, op := range q.operations {
		switch op.action {
		case uoNoticeRead, uoNoticeUnread, uoNoticeArchive, uoNoticeUnarchive:
			if op.through != 0 || len(op.targets) == 0 {
				return ErrInvalid
			}
		case uoNoticeReadThrough:
			if op.through < 0 || len(op.targets) != 0 {
				return ErrInvalid
			}
		default:
			return ErrInvalid
		}
		targets += len(op.targets)
		if targets > uoNoticeMaxAckTargets {
			return ErrInvalid
		}
		for _, target := range op.targets {
			if !uoNoticeIdentifier(target.id, uoNoticeMaxIDBytes) || target.revision <= 0 || target.authorityRevision <= 0 {
				return ErrInvalid
			}
		}
	}
	return nil
}

// Same rule as the merged read/uo_notice_state.go, kept native and values-only
// across the package boundary. Malformed durable facts are a resync, never an
// invented read: a positive head with a zero time cannot synthesize epoch
// readAt, and a raw sequence-zero head is no bulk coverage.
func resolveUONativeAck(sequence, revision int64, individual uoNoticeAckState, head uoNoticeAckHead) (read, archive sql.NullInt64, err error) {
	validTime := func(v sql.NullInt64) bool {
		return v.Valid && v.Int64 >= 0 || !v.Valid && v.Int64 == 0
	}
	if sequence <= 0 || revision <= 0 ||
		!individual.exists && individual != (uoNoticeAckState{}) ||
		individual.exists && (individual.revision <= 0 || !validTime(individual.readMS) || !validTime(individual.archivedMS)) ||
		!head.exists && head != (uoNoticeAckHead{}) ||
		head.exists && (head.sequence <= 0 || head.revision <= 0 || head.readMS <= 0) {
		return read, archive, ErrResolveRequired
	}
	current := individual.exists && individual.revision >= revision
	covered := head.exists && sequence <= head.sequence && revision <= head.revision
	if current {
		archive = individual.archivedMS
		if individual.readMS.Valid {
			// Even a later read-through keeps this individual's exact time.
			return individual.readMS, archive, nil
		}
		if individual.archivedMS.Valid || !covered || individual.revision > head.revision {
			// Read-through excludes archived notices, and an explicit unread
			// after bulk coverage stays unread.
			return read, archive, nil
		}
	}
	if covered {
		read = sql.NullInt64{Int64: head.readMS, Valid: true}
	}
	return read, archive, nil
}

// One indexed read of this principal's read-through head. Another principal's
// head is a different primary key and is never selected.
func loadUONoticeAckHead(ctx context.Context, turn *commandTurn, principal string) (out uoNoticeAckHead, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeAckHeadRead, principal)
	if err != nil {
		return out, err
	}
	var archive int64
	err = row.Scan(&out.sequence, &archive, &out.revision, &out.readMS)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeAckHead{}, nil
	}
	if err != nil {
		return out, err
	}
	// A raw zero-sequence head is a normalized empty head only when its time is
	// zero too; a positive time would be coverage with no boundary.
	if archive != 0 || out.sequence < 0 || out.revision <= 0 || out.readMS < 0 ||
		out.sequence > 0 && out.readMS == 0 || out.sequence == 0 && out.readMS != 0 {
		return uoNoticeAckHead{}, ErrResolveRequired
	}
	if out.sequence == 0 {
		return uoNoticeAckHead{}, nil
	}
	out.exists = true
	return out, nil
}

// The highest sequence in this exact authorized inbox, withdrawn and expired
// rows included. It bounds a read-through so a caller can never cover a notice
// that does not exist yet, and no other inbox is visible.
func loadUONoticeAckUpper(ctx context.Context, turn *commandTurn, view uoNoticeView) (upper int64, err error) {
	defer func() { upper, err = finishCommand(ctx, commandLifetime(turn), upper, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeAckUpper, view.scope, view.audience)
	if err != nil {
		return 0, err
	}
	err = row.Scan(&upper)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if upper <= 0 {
		return 0, ErrResolveRequired
	}
	return upper, nil
}

// One scoped point lookup by public identifier. A record in another inbox is
// indistinguishable from an absent one, and a withdrawn or already expired
// record is not an actionable target.
func loadUONoticeAckTarget(ctx context.Context, turn *commandTurn, view uoNoticeView, id string, nowMS int64) (out uoNoticeRecordState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeAckTargetRead, id, view.scope, view.audience)
	if err != nil {
		return out, err
	}
	var sequence, revision, authorityRevision, expiresMS int64
	var withdrawn sql.NullInt64
	err = row.Scan(&sequence, &revision, &authorityRevision, &expiresMS, &withdrawn)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeRecordState{}, nil
	}
	if err != nil {
		return out, err
	}
	if sequence <= 0 || revision <= 0 || authorityRevision <= 0 || expiresMS <= 0 {
		return uoNoticeRecordState{}, ErrResolveRequired
	}
	if withdrawn.Valid || expiresMS <= nowMS {
		return uoNoticeRecordState{}, nil
	}
	out.exists, out.id, out.sequence, out.revision, out.authorityRevision = true, id, sequence, revision, authorityRevision
	return out, nil
}

// One indexed read of this principal's individual acknowledgement. A different
// principal is a different primary key and is never selected.
func loadUONoticeAckRow(ctx context.Context, turn *commandTurn, principal string, sequence int64) (out uoNoticeAckState, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	row, err := wc008DraftQuery(ctx, turn, uoNoticeAckRowRead, principal, sequence)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.revision, &out.readMS, &out.archivedMS)
	if errors.Is(err, sql.ErrNoRows) {
		return uoNoticeAckState{}, nil
	}
	if err != nil {
		return out, err
	}
	if out.revision <= 0 || out.readMS.Valid && out.readMS.Int64 < 0 || out.archivedMS.Valid && out.archivedMS.Int64 < 0 {
		return uoNoticeAckState{}, ErrResolveRequired
	}
	out.exists = true
	return out, nil
}

// Fresh-only component after current authority and receipt classification.
// Planned outcomes are complete: every input operation produces exactly one
// outcome, in public order, carrying its own ordinal. Only real changes write
// an acknowledgement row; one changed command appends one hint.
func applyUONoticeAcks(ctx context.Context, turn *commandTurn, q uoNoticeAckInput) (out uoNoticeAckEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	head, err := loadUONoticeAckHead(ctx, turn, q.principal)
	if err != nil {
		return out, err
	}
	first, err := reserveUONoticeOrdinals(ctx, turn, len(q.operations))
	if err != nil {
		return out, err
	}
	if head.exists && first <= head.revision {
		return out, ErrResolveRequired
	}
	for i, op := range q.operations {
		ordinal := first + int64(i)
		if op.action == uoNoticeReadThrough {
			result := uoNoticeAckOutcome{action: op.action, revision: ordinal}
			upper, e := loadUONoticeAckUpper(ctx, turn, q.view)
			if e != nil {
				return out, e
			}
			through := op.through
			if through == 0 {
				through = upper
			}
			// A boundary beyond the newest record in this inbox would cover a
			// notice that does not exist yet; refuse the stale scalar.
			if through > upper {
				return out, ErrConflict
			}
			result.through = through
			if upper > 0 {
				next := head
				if through > next.sequence {
					next.sequence = through
				}
				if q.nowMS > next.readMS {
					next.readMS = q.nowMS
				}
				next.exists, next.revision = true, ordinal
				if _, e = wc008DraftInteger(ctx, turn, uoNoticeAckHeadUpsert, q.principal, next.sequence, ordinal, next.readMS); e != nil {
					return out, e
				}
				head = next
				out.changed++
				result.changed = true
			}
			out.outcomes = append(out.outcomes, result)
			continue
		}
		for _, target := range op.targets {
			result := uoNoticeAckOutcome{action: op.action, id: target.id, revision: ordinal}
			record, e := loadUONoticeAckTarget(ctx, turn, q.view, target.id, q.nowMS)
			if e != nil {
				return out, e
			}
			if !record.exists {
				result.notFound = true
				out.outcomes = append(out.outcomes, result)
				continue
			}
			if ordinal <= record.revision {
				return out, ErrResolveRequired
			}
			if record.revision != target.revision || record.authorityRevision != target.authorityRevision {
				return out, ErrConflict
			}
			before, e := loadUONoticeAckRow(ctx, turn, q.principal, record.sequence)
			if e != nil {
				return out, e
			}
			read, archive, e := resolveUONativeAck(record.sequence, record.revision, before, head)
			if e != nil {
				return out, e
			}
			nextRead, nextArchive := read, archive
			switch op.action {
			case uoNoticeRead:
				if !read.Valid {
					nextRead = sql.NullInt64{Int64: q.nowMS, Valid: true}
				}
			case uoNoticeUnread:
				nextRead = sql.NullInt64{}
			case uoNoticeArchive:
				if !archive.Valid {
					nextArchive = sql.NullInt64{Int64: q.nowMS, Valid: true}
				}
				// Match the legacy COALESCE(read_ms,now), including a repeated
				// archive after marking an already archived notice unread.
				if !read.Valid {
					nextRead = sql.NullInt64{Int64: q.nowMS, Valid: true}
				}
			case uoNoticeUnarchive:
				nextArchive = sql.NullInt64{}
			}
			result.readMS, result.archivedMS = nextRead, nextArchive
			// A no-op action must not freeze a bulk-only timestamp into a
			// sparse individual read.
			if nextRead != read || nextArchive != archive {
				if _, e = wc008DraftInteger(ctx, turn, uoNoticeAckRowUpsert, q.principal, record.sequence,
					uoNoticeNullable(nextRead), uoNoticeNullable(nextArchive), ordinal); e != nil {
					return out, e
				}
				result.changed = true
				out.changed++
			}
			out.outcomes = append(out.outcomes, result)
		}
	}
	out.revision = first + int64(len(q.operations)) - 1
	if out.changed > 0 {
		if out.event, err = appendUONoticeHint(ctx, turn, q.view, out.revision, q.nowMS); err != nil {
			return out, err
		}
	}
	return out, turn.checkLifetimes(ctx)
}

func uoNoticeNullable(v sql.NullInt64) any {
	if v.Valid {
		return v.Int64
	}
	return nil
}
