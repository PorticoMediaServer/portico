package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"unicode/utf8"
)

type uoFlagValue struct{ set, value bool }
type uoRatingValue struct {
	set   bool
	value sql.NullInt64
}
type uoPersonalFlagsPatch struct {
	watchlisted, favorite, notInterested uoFlagValue
	rating                               uoRatingValue
}

type uoPersonalFlagsState struct {
	revision                             int64
	watchlisted, favorite, notInterested bool
	rating                               sql.NullInt64
}

// These are native facts from the SAME freshly authorized writer turn. They
// are not an authorization capability, command receipt or public input shape.
type uoPersonalFlagsInput struct {
	profileKey, libraryKey               string
	subjectID, libraryID, entityRevision int64
	admissionRevision, expectedRevision  int64
	publicID                             [16]byte
	patch                                uoPersonalFlagsPatch
}

// This is an internal effect, never an Applied result. The sole dispatcher
// still owns current disclosure, original receipt classification, exact quota,
// saved result, bounded event append and the physical commit/rollback path.
type uoPersonalFlagsEffect struct {
	state                             uoPersonalFlagsState
	changed                           bool
	viewerRevision, selectionRevision int64
}

func (q uoPersonalFlagsInput) validate() error {
	if q.profileKey == "" || len(q.profileKey) > 512 || !utf8.ValidString(q.profileKey) ||
		q.libraryKey == "" || len(q.libraryKey) > 128 || !utf8.ValidString(q.libraryKey) ||
		q.subjectID <= 0 || q.libraryID <= 0 || q.entityRevision <= 0 || q.admissionRevision <= 0 || q.expectedRevision < 0 ||
		q.publicID == [16]byte{} {
		return ErrInvalid
	}
	p := q.patch
	if !p.watchlisted.set && !p.favorite.set && !p.notInterested.set && !p.rating.set ||
		!p.watchlisted.set && p.watchlisted.value || !p.favorite.set && p.favorite.value || !p.notInterested.set && p.notInterested.value ||
		!p.rating.set && p.rating.value != (sql.NullInt64{}) ||
		!p.rating.value.Valid && p.rating.value.Int64 != 0 || p.rating.value.Valid && (p.rating.value.Int64 < 0 || p.rating.value.Int64 > 10) {
		return ErrInvalid
	}
	return nil
}

func (p uoPersonalFlagsPatch) apply(before uoPersonalFlagsState) uoPersonalFlagsState {
	after := before
	if p.watchlisted.set {
		after.watchlisted = p.watchlisted.value
	}
	if p.favorite.set {
		after.favorite = p.favorite.value
	}
	if p.notInterested.set {
		after.notInterested = p.notInterested.value
	}
	if p.rating.set {
		after.rating = p.rating.value
	}
	return after
}

// Fresh-only component after current authority and receipt classification.
// The caller must roll back the ENTIRE turn on any error. Replay must never
// enter this function. Watched, resume, activity and immutable history are not
// rewritten by a flag patch; only this profile's sparse row and head change.
func applyUOPersonalFlags(ctx context.Context, turn *commandTurn, q uoPersonalFlagsInput) (out uoPersonalFlagsEffect, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	// Recheck native identity/source at the effect boundary. The independent
	// dispatcher, not this subject witness, supplies current viewing authority.
	if _, err = wc008DraftInteger(ctx, turn, uoPersonalFlagsSubject, q.subjectID, q.libraryID, q.publicID[:], q.entityRevision, q.admissionRevision, q.libraryKey); err != nil {
		return out, err
	}
	row, err := wc008DraftQuery(ctx, turn, uoPersonalFlagsRead, q.profileKey, q.subjectID, q.publicID[:])
	if err != nil {
		return out, err
	}
	var before uoPersonalFlagsState
	err = row.Scan(&before.revision, &before.watchlisted, &before.favorite, &before.notInterested, &before.rating)
	if errors.Is(err, sql.ErrNoRows) {
		before = uoPersonalFlagsState{}
	} else if err != nil {
		return out, err
	}
	if before.revision != q.expectedRevision {
		return out, ErrConflict
	}
	after := q.patch.apply(before)
	out.state = after
	if after == before {
		return out, nil
	}
	if before.revision == math.MaxInt64 {
		return out, ErrConflict
	}
	after.revision++
	rating := any(nil)
	if after.rating.Valid {
		rating = after.rating.Int64
	}
	flags := []any{uoFlagInteger(after.watchlisted), uoFlagInteger(after.favorite), uoFlagInteger(after.notInterested), rating}
	if before.revision == 0 {
		args := []any{q.profileKey, q.publicID[:], q.subjectID, q.libraryID}
		args = append(args, flags...)
		_, err = wc008DraftInteger(ctx, turn, uoPersonalFlagsInsert, args...)
	} else {
		args := []any{after.revision}
		args = append(args, flags...)
		args = append(args, q.profileKey, q.subjectID, q.publicID[:], before.revision)
		_, err = wc008DraftInteger(ctx, turn, uoPersonalFlagsUpdate, args...)
	}
	if err != nil {
		return out, err
	}
	row, err = wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionRead, q.profileKey, q.libraryKey)
	if err != nil {
		return out, err
	}
	var oldViewer, oldSelection int64
	err = row.Scan(&oldViewer, &oldSelection)
	missingHead := errors.Is(err, sql.ErrNoRows)
	if missingHead {
		oldViewer, oldSelection = 0, 0
	} else if err != nil {
		return out, err
	}
	if oldViewer < 0 || oldSelection < 0 || oldViewer == math.MaxInt64 || oldSelection == math.MaxInt64 {
		return out, ErrConflict
	}
	if missingHead {
		row, err = wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionInsert, q.profileKey, q.libraryKey, int64(1), int64(1))
	} else {
		row, err = wc008DraftQuery(ctx, turn, uoPersonalFlagsRevisionUpdate, oldViewer+1, oldSelection+1, q.profileKey, q.libraryKey, oldViewer, oldSelection)
	}
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.viewerRevision, &out.selectionRevision); errors.Is(err, sql.ErrNoRows) {
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if out.viewerRevision <= 0 || out.selectionRevision <= 0 {
		return out, ErrResolveRequired
	}
	out.state, out.changed = after, true
	return out, turn.checkLifetimes(ctx)
}

func uoFlagInteger(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
