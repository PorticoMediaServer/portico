package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Canonical-v1 components only. Production identity/catalog/usermutation still
// use persistence.Open: those legacy callers MUST NOT call these participants.
// The parent owns current authorization, fresh classification, admission,
// accounting, cursor/receipt/event and physical commit. These comparison inputs
// grant no deletion permission. Every error requires whole-turn rollback.
type uoReadingPruneKind uint8

const (
	uoReadingPruneAccount uoReadingPruneKind = iota + 1
	uoReadingPruneFamily
	uoReadingPruneOrphan
	uoReadingPruneGlobal
	uoReadingPruneProfile // US erasure: exact old profile lifetime, not a cutoff.
)

type uoReadingPruneScope struct {
	kind                                          uoReadingPruneKind
	scopeID                                       int64
	authority, account, family, profile, lifetime string
}

type uoReadingPrunedCharge struct {
	scopeID, readingBytes int64
	stop                  bool
}

type uoReadingPruneEffect struct {
	charges                                                  [32]uoReadingPrunedCharge // exact selected account/partition for parent quota CAS
	receipts, readingResults, variableBytes, ordinary, stops int64
}

// The four public-receipt pruners select ONCE, before any child deletion.
// Never rerun a LIMIT selection after deleting children or choose children by
// age/profile independently. The bounded keys survive only inside this turn.
func pruneUOReadingReceipts(ctx context.Context, turn *commandTurn, q uoReadingPruneScope) (out uoReadingPruneEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	var id stmt.ID
	var args []any
	switch q.kind {
	case uoReadingPruneAccount:
		if q.scopeID <= 0 {
			return out, ErrInvalid
		}
		cut, e := wc008DraftInteger(ctx, turn, uoReadingAccountCut, q.scopeID)
		if e != nil {
			return out, e
		}
		id, args = uoReadingSelectAccount, []any{q.scopeID, cut}
	case uoReadingPruneFamily:
		if (q.authority != "local" && q.authority != "hosted") || !usIdentifier(q.account) || !usIdentifier(q.family) {
			return out, ErrInvalid
		}
		row, e := wc008DraftQuery(ctx, turn, uoReadingFamilyCut, q.authority, q.account)
		if e != nil {
			return out, e
		}
		var scope, cut int64
		if e = row.Scan(&scope, &cut); e != nil {
			return out, e
		}
		global, e := wc008DraftInteger(ctx, turn, uoReadingGlobalCut)
		if e != nil {
			return out, e
		}
		id, args = uoReadingSelectFamily, []any{scope, q.family, max(cut, global)}
	case uoReadingPruneOrphan:
		if q.scopeID <= 0 {
			return out, ErrInvalid
		}
		if _, e := wc008DraftInteger(ctx, turn, uoReadingOrphanProof, q.scopeID); e != nil {
			return out, e
		}
		id, args = uoReadingSelectOrphan, []any{q.scopeID}
	case uoReadingPruneGlobal:
		cut, e := wc008DraftInteger(ctx, turn, uoReadingGlobalCut)
		if e != nil {
			return out, e
		}
		id, args = uoReadingSelectGlobal, []any{cut}
	case uoReadingPruneProfile:
		if q.scopeID <= 0 || !usIdentifier(q.profile) || !usHex(q.lifetime, 32) {
			return out, ErrInvalid
		}
		id, args = uoReadingSelectProfile, []any{q.scopeID, q.profile, q.lifetime}
	default:
		return out, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return out, ErrInvalid
	}
	rows, err := turn.query(ctx, step.statementStep, args...)
	if err != nil {
		return out, err
	}
	var keys [32]string
	n := 0
	for rows.Next() {
		if n == len(keys) {
			_ = rows.Close()
			return out, ErrConflict
		}
		if err = rows.Scan(&keys[n]); err != nil {
			_ = rows.Close()
			return out, err
		}
		if len(keys[n]) != 51 {
			_ = rows.Close()
			return out, ErrConflict
		}
		n++
	}
	if err = rows.Err(); err != nil {
		_ = rows.Close()
		return out, err
	}
	if err = rows.Close(); err != nil {
		return out, err
	}
	for ordinal, key := range keys[:n] {
		row, e := wc008DraftQuery(ctx, turn, uoReadingResultBytes, key)
		if e != nil {
			return out, e
		}
		var size int64
		e = row.Scan(&size)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return out, e
		}
		if e == nil {
			if size < 0 {
				return out, ErrConflict
			}
			removed, e := wc008DraftInteger(ctx, turn, uoReadingResultDelete, key, size)
			if e != nil {
				return out, e
			}
			if removed != size {
				return out, ErrConflict
			}
			out.readingResults++
			out.variableBytes += removed
		}
		parent, e := wc008DraftQuery(ctx, turn, uoReadingReceiptDelete, key)
		if e != nil {
			return out, e
		}
		var stop, scope int64
		if e = parent.Scan(&stop, &scope); e != nil {
			return out, e
		}
		if scope <= 0 || (stop != 0 && stop != 1) {
			return out, ErrConflict
		}
		out.charges[ordinal] = uoReadingPrunedCharge{scopeID: scope, readingBytes: size, stop: stop == 1}

		out.receipts++
		if stop == 1 {
			out.stops++
		} else {
			out.ordinary++
		}
	}
	return out, turn.checkLifetimes(ctx)
}

type uoReadingPersonalScope struct {
	personalID, itemID, libraryID int64
	profileKey                    string
	publicID                      [16]byte
}

type uoReadingResumeDrainEffect struct {
	removed, variableBytes int64
	hasMore                bool
}

func (s uoReadingPersonalScope) valid() bool {
	return s.personalID > 0 && s.itemID > 0 && s.libraryID > 0 && len(s.profileKey) > 0 && len(s.profileKey) <= 512 && s.publicID != [16]byte{}
}

func checkUOReadingPersonal(ctx context.Context, turn *commandTurn, s uoReadingPersonalScope) error {
	if !s.valid() {
		return ErrInvalid
	}
	_, err := wc008DraftInteger(ctx, turn, uoReadingPersonalPoint, s.personalID, s.profileKey, s.itemID, s.libraryID, s.publicID[:])
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Exactly one already-selected personal parent's child per quantum. Asset,
// source or library retirement is NOT permission to erase a personal parent.
// No asset lookup/FK: stale source positions remain until their actual personal
// parent is erased. hasMore must be false before the owning parent may delete.
func drainUOReadingResume(ctx context.Context, turn *commandTurn, s uoReadingPersonalScope) (out uoReadingResumeDrainEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	if err = checkUOReadingPersonal(ctx, turn, s); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoReadingResumeHead, s.personalID)
	if err != nil {
		return out, err
	}
	var asset, size, observed, revision int64
	err = row.Scan(&asset, &size, &observed, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, turn.checkLifetimes(ctx)
	}
	if err != nil {
		return out, err
	}
	removed, err := wc008DraftInteger(ctx, turn, uoReadingResumeDelete, s.personalID, asset, size)
	if err != nil {
		return out, err
	}
	if removed != size {
		return out, ErrConflict
	}
	more, err := uoReadingResumeHasMore(ctx, turn, s.personalID)
	if err != nil {
		return out, err
	}
	out = uoReadingResumeDrainEffect{removed: 1, variableBytes: removed, hasMore: more}
	return out, turn.checkLifetimes(ctx)
}

// One-source title-merge participant. Preserve every source position; for the
// same asset the latest observation wins, then the higher source revision,
// with exact ties retaining the destination. The parent must co-commit its
// personal/reading revision CAS and remaining title state before deleting from.
// This does NOT erase either parent or grant title-merge authority.
func mergeUOReadingResume(ctx context.Context, turn *commandTurn, from, to uoReadingPersonalScope) (out uoReadingResumeDrainEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if from.personalID == to.personalID || from.profileKey != to.profileKey || from.libraryID != to.libraryID {
		return out, ErrInvalid
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	for _, s := range []uoReadingPersonalScope{from, to} {
		if err = checkUOReadingPersonal(ctx, turn, s); err != nil {
			return out, err
		}
	}
	row, err := wc008DraftQuery(ctx, turn, uoReadingResumeHead, from.personalID)
	if err != nil {
		return out, err
	}
	var asset, size, observed, revision int64
	err = row.Scan(&asset, &size, &observed, &revision)
	if errors.Is(err, sql.ErrNoRows) {
		return out, turn.checkLifetimes(ctx)
	}
	if err != nil {
		return out, err
	}
	row, err = wc008DraftQuery(ctx, turn, uoReadingResumeTarget, to.personalID, asset)
	if err != nil {
		return out, err
	}
	var targetObserved, targetRevision, targetBytes int64
	err = row.Scan(&targetObserved, &targetRevision, &targetBytes)
	absent := errors.Is(err, sql.ErrNoRows)
	if err != nil && !absent {
		return out, err
	}
	sourceWins := absent || observed > targetObserved || (observed == targetObserved && revision > targetRevision)
	if sourceWins {
		if !absent {
			if _, err = wc008DraftInteger(ctx, turn, uoReadingResumeDelete, to.personalID, asset, targetBytes); err != nil {
				return out, err
			}
		}
		if _, err = wc008DraftInteger(ctx, turn, uoReadingResumeMerge, to.personalID, from.personalID, asset, size); err != nil {
			return out, err
		}
	} else {
		if _, err = wc008DraftInteger(ctx, turn, uoReadingResumeDelete, from.personalID, asset, size); err != nil {
			return out, err
		}
	}

	more, err := uoReadingResumeHasMore(ctx, turn, from.personalID)
	if err != nil {
		return out, err
	}
	// Merge relocation is not a quota refund; target delta belongs to its parent.
	out = uoReadingResumeDrainEffect{removed: 1, hasMore: more}
	return out, turn.checkLifetimes(ctx)
}

func uoReadingResumeHasMore(ctx context.Context, turn *commandTurn, personal int64) (bool, error) {
	row, err := wc008DraftQuery(ctx, turn, uoReadingResumeRemaining, personal)
	if err != nil {
		return false, err
	}
	var yes int64
	err = row.Scan(&yes)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return yes == 1, err
}
