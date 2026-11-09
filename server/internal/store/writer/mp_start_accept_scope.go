package writer

import (
	"context"
	"errors"
	"time"
)

// checkMPStartAcceptScope copies MP-07's same-turn authority recipe, using
// MP-04's exact registered statements. It proves CURRENT membership, profile
// restrictions and the CURRENT token before any 703/704/705 owner or receipt
// disclosure. Only the durable session binds the current credential to the
// original command: refresh never changes that command or its digest.
func checkMPStartAcceptScope(ctx context.Context, t *commandTurn, d *mpStartPrepDisclosure, f mpStartAcceptFence, now int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if d == nil || !f.valid() || !mpPrepTexts(d.library) || !d.actor.sameSession(f.actor) || !d.actor.valid(now) {
		return ErrDisclosureDenied
	}
	if source := f.source; !mpPrepTexts(source.library) || source.library != d.library {
		return ErrDisclosureDenied
	}
	s := d.scope
	if !s.valid() {
		return ErrDisclosureDenied
	}
	scope := d.actor.nativeScope(s, d.library)
	if !wcProgressLibraryAllowed(scope) {
		return ErrDisclosureDenied
	}
	id := wcProgressNativeScope
	args := []any{scope.accountID, scope.profileID, scope.memberBirth, scope.profileBirth, scope.memberLibraries,
		scope.memberRevision, scope.profileRevision, scope.membershipRole, scope.primary, scope.primaryProfile}
	if scope.profileLibraries.Valid {
		id = wcProgressNativeProfileScope
		args = append(args, scope.profileLibraries.String)
	}
	row, err := wc008DraftQuery(ctx, t, id, args...)
	if err != nil {
		return err
	}
	var epoch, primary int64
	if err = row.Scan(&epoch, &primary); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	if epoch != scope.epoch || scope.role != "member" && (primary == 0 || scope.membershipRole != scope.role) {
		return ErrDisclosureDenied
	}
	row, err = wc008DraftQuery(ctx, t, wcProgressNativeToken, scope.tokenHash, scope.familyID, scope.serverID,
		scope.accountID, scope.profileID, scope.role, scope.epoch, scope.tokenGeneration,
		scope.memberBirth, scope.profileBirth, scope.horizon, scope.expires)
	if err != nil {
		return err
	}
	var generation int64
	if err = row.Scan(&generation); err != nil {
		return errors.Join(ErrDisclosureDenied, err)
	}
	horizon, e := time.Parse(time.RFC3339Nano, scope.horizon)
	if e != nil {
		return ErrDisclosureDenied
	}
	expires, e := time.Parse(time.RFC3339Nano, scope.expires)
	if e != nil {
		return ErrDisclosureDenied
	}
	deadline, ok := t.transaction.ctx.Deadline()
	if !ok || generation != scope.tokenGeneration || deadline.After(horizon) || deadline.After(expires) {
		return ErrDisclosureDenied
	}
	return t.checkLifetimes(ctx)
}
