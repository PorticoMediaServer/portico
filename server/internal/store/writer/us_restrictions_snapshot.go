package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The complete current-source predecessor of ONE profile-restrictions
// replacement. It is read in the same frame as the write and is never retained.
//
// The US-01 owner stamp already proves a current direct-owner main-profile
// interactive session. This file adds the product's own narrowing on top: the
// CURRENT family role must still equal the CURRENT membership role
// (identity.directCallerTx), and this first recipe admits only the owner role
// (the recording grant). A caller whose account primary profile is an admin or
// member is explicitly refused here rather than given an invented permission;
// a later widening must add its own reviewed role branch and ownerGrant read.
type usRestrictionsBefore struct {
	restricted bool
	revision   int64
	allowDVR   bool
	// ownerGrant is true only because usRestrictionsScopeCheck proved
	// m.role='owner' for the exact current account/family. The product allows
	// only the server owner to switch Recordings ON.
	ownerGrant bool
}

// The exact post-effect row the replacement wrote. Every field is compared to
// the frozen preparation, so the saved result is the complete response DTO and
// never a source-reconstructed guess.
type usRestrictionsEffect struct {
	revision                                                                int64
	ratingSystem, maximumAgeRating, blocked                                 string
	maximumAge                                                              int64
	allowUnrated, allowDownloads, allowLiveTV, allowDVR, allowWatchTogether bool
}

// readUSRestrictionsFreshTarget reads the current target exactly once, before
// the receipt reservation and before any effect. A stale revision is a conflict;
// a deleted profile, a foreign account, a changed lifetime or a session whose
// role no longer matches its membership is a denial with no enumeration detail.
func readUSRestrictionsFreshTarget(ctx context.Context, turn *commandTurn, a *usOwnerAuthorization, input *usRestrictionsCommand) (out usRestrictionsBefore, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if turn == nil || turn.transaction == nil || a == nil || input == nil || input.kind != USProfileRestrictions {
		return out, ErrInvalid
	}
	q := input.request
	issuer := a.proof.witness.issuer
	// The product edits only a profile of the CALLER's own account. A profile id
	// from another account is a different operation and is not admitted here.
	// Legacy admin/member main-profile and account-scoped session paths are
	// refused ON PURPOSE by the owner-role scope check below; they must be
	// widened deliberately (with their own reviewed role branch and ownerGrant
	// read) before any product wiring, never admitted by coincidence.
	if q.account != issuer.account {
		return out, ErrDisclosureDenied
	}
	// Current session/membership role narrowing and the owner recording grant,
	// compared inside SQLite before any source row is materialized. The member
	// incarnation is the SAME witness US-05/US-09 CAS in SQL: a request carrying
	// an old membership's lifetime with a current issuer is refused here rather
	// than saved as the member lifetime that made the change.
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsScopeCheck, q.account, issuer.family, q.memberIncarnation)
	if err != nil {
		return out, err
	}
	var scope int64
	if err = row.Scan(&scope); errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	} else if err != nil {
		return out, err
	}
	if scope != 1 {
		return out, ErrResolveRequired
	}
	out.ownerGrant = true
	row, err = usRestrictionsRow(ctx, turn, usRestrictionsTargetFacts, q.account, q.profile, q.profileLifetime)
	if err != nil {
		return out, err
	}
	var deleted, primary int64
	if err = row.Scan(&deleted, &primary); errors.Is(err, sql.ErrNoRows) {
		return out, ErrDisclosureDenied
	} else if err != nil {
		return out, err
	}
	if deleted != 0 || (primary != 0 && primary != 1) {
		return out, ErrDisclosureDenied
	}
	row, err = usRestrictionsRow(ctx, turn, usRestrictionsCurrentRow, q.profile)
	if err != nil {
		return out, err
	}
	var revision, dvr int64
	switch scan := row.Scan(&revision, &dvr); {
	case errors.Is(scan, sql.ErrNoRows):
		// A missing row is the product's documented default: nothing blocked at
		// revision 1. Recording starts on only for the owner's main profile.
		out.restricted = false
		out.revision = 1
		out.allowDVR = primary == 1
	default:
		if scan != nil {
			return out, scan
		}
		if revision < 1 || revision >= usMaxRevision || (dvr != 0 && dvr != 1) {
			return out, ErrResolveRequired
		}
		out.restricted = true
		out.revision = revision
		out.allowDVR = dvr == 1
	}
	if revision := input.request.expectedRevision; revision != out.revision {
		return out, ErrConflict
	}
	// Who can record: only the owner may turn Recordings on. Switching it off is
	// open to any manager. This recipe's scope proof is the owner role, so the
	// branch is unreachable today, but a widened recipe cannot skip it.
	if !usRestrictionsDVRAllowed(out.allowDVR, input.request.allowDVR, out.ownerGrant) {
		return out, ErrDisclosureDenied
	}
	return out, turn.checkLifetimes(ctx)
}

// usRestrictionsDVRAllowed is identity.writeRestrictionsTx's recording grant
// rule: switching Recordings on needs the server owner; leaving it on or
// switching it off does not.
func usRestrictionsDVRAllowed(before, requested, ownerGrant bool) bool {
	return !requested || before || ownerGrant
}

// applyUSRestrictions is the ONE replacement of the source row, never Applied
// and never a replay. The caller supplies only the complete verified projection
// and the fresh predecessor from the SAME frame. A replay returns the retained
// receipt and MUST NOT enter this function: it never reconstructs an original
// result from current restrictions.
func applyUSRestrictions(ctx context.Context, turn *commandTurn, input *usRestrictionsCommand, before usRestrictionsBefore) (out usRestrictionsEffect, err error) {
	defer func() {
		v, e := finishCommand(ctx, commandLifetime(turn), out, err)
		out, err = v, e
	}()
	if turn == nil || turn.transaction == nil || input == nil || input.kind != USProfileRestrictions {
		return out, ErrInvalid
	}
	if turn.transaction.descriptor.ID != input.kind || before.revision != input.request.expectedRevision || before.revision < 1 || before.revision >= usMaxRevision {
		return out, ErrConflict
	}
	q := input.request
	want := usRestrictionsEffect{revision: before.revision + 1, ratingSystem: q.ratingSystem, maximumAgeRating: q.maximumAgeRating,
		maximumAge: input.maximumAge, blocked: input.blockedJSON, allowUnrated: q.allowUnrated, allowDownloads: q.allowDownloads,
		allowLiveTV: q.allowLiveTV, allowDVR: q.allowDVR, allowWatchTogether: q.allowWatchTogether}
	if err = turn.checkLifetimes(ctx); err != nil {
		return out, err
	}
	row, err := usRestrictionsReplacement(ctx, turn, input, before)
	if err != nil {
		return out, err
	}
	var revision, maximumAge, unrated, downloads, live, dvr, together int64
	var system, code, blocked string
	if err = row.Scan(&revision, &system, &code, &maximumAge, &unrated, &blocked, &downloads, &live, &dvr, &together); errors.Is(err, sql.ErrNoRows) {
		// The update's CAS/RETURNING matched no row: the predecessor is stale.
		return out, ErrConflict
	} else if err != nil {
		return out, err
	}
	if revision != want.revision || system != want.ratingSystem || code != want.maximumAgeRating || maximumAge != want.maximumAge ||
		blocked != want.blocked || unrated != usRestrictionsBool(want.allowUnrated) || downloads != usRestrictionsBool(want.allowDownloads) ||
		live != usRestrictionsBool(want.allowLiveTV) || dvr != usRestrictionsBool(want.allowDVR) || together != usRestrictionsBool(want.allowWatchTogether) {
		return out, ErrResolveRequired
	}
	return want, turn.checkLifetimes(ctx)
}

// usRestrictionsReplacement chooses the CAS UPDATE when the profile already has
// a stored document, or the INSERT when it does not. Both write the same eleven
// columns. BEGIN IMMEDIATE serializes writers, so the absence proved by the
// fresh read in this frame cannot turn into a second row.
func usRestrictionsReplacement(ctx context.Context, turn *commandTurn, input *usRestrictionsCommand, before usRestrictionsBefore) (commandRow, error) {
	q := input.request
	if !before.restricted {
		return usRestrictionsRow(ctx, turn, usRestrictionsInsert, q.profile, q.ratingSystem, q.maximumAgeRating, input.maximumAge,
			usRestrictionsBool(q.allowUnrated), input.blockedJSON, usRestrictionsBool(q.allowDownloads), usRestrictionsBool(q.allowLiveTV),
			usRestrictionsBool(q.allowDVR), usRestrictionsBool(q.allowWatchTogether), before.revision+1)
	}
	row, err := usRestrictionsRow(ctx, turn, usRestrictionsUpdate, q.ratingSystem, q.maximumAgeRating, input.maximumAge,
		usRestrictionsBool(q.allowUnrated), input.blockedJSON, usRestrictionsBool(q.allowDownloads), usRestrictionsBool(q.allowLiveTV),
		usRestrictionsBool(q.allowDVR), usRestrictionsBool(q.allowWatchTogether), before.revision+1, q.profile, before.revision)
	if errors.Is(err, sql.ErrNoRows) {
		return commandRow{}, ErrConflict
	}
	return row, err
}

func usRestrictionsBool(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func usRestrictionsRow(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	if turn == nil {
		return commandRow{}, ErrInvalid
	}
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, invalid("us_restrictions_statement")
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}
