package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// IP's first owner-authorized recipe uses US's exact current-source predicates,
// not its FULL540/541 authorization stamp. No domain range or caller bool is
// permission. This prefix is private and is not a command body or Applied result.
// A caller must include these definitions/work in its one installed W plan.
type ipEntryBudget struct{ memory uint64 }

func (b ipEntryBudget) check(ctx context.Context, turn *commandTurn, bytes int64) error {
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	tx := turn.transaction
	switch tx.descriptor.ID {
	case IPFieldEdit, IPFieldLock, IPFieldRestore, IPEditBulkBegin, IPEditBulkPage, IPEditBulkFinish:
	default:
		return ErrInvalid
	}
	d, err := Describe(tx.descriptor.ID)
	if err != nil || d != tx.descriptor || !tx.begun || tx.done || !tx.policyVerified {
		return ErrInvalid
	}
	sync := int(sqliteconn.Normal)
	if d.Durability == Full {
		sync = int(sqliteconn.Full)
	}
	if tx.settings.Synchronous != sync {
		return ErrInvalid
	}
	if bytes < 0 || bytes > maxCanonicalResultBytes || uint64(64<<10)+uint64(bytes)*32 > b.memory {
		return &Refusal{Code: RefusalCapacity}
	}
	g, ok := tx.custody.(*governedTurn)
	if !ok || g == nil || g.g == nil || g.a == nil || g.grant == nil {
		return &Refusal{Code: RefusalUnqualified}
	}
	if err := g.check(d.ID, tx.workLimit); err != nil {
		return err
	}
	if g.expected.Need[governor.MemoryBytes] <= 0 || b.memory > uint64(g.expected.Need[governor.MemoryBytes]) {
		return &Refusal{Code: RefusalUnqualified}
	}
	return turn.checkLifetimes(ctx)
}
func authorizeIPEntryOwner(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b ipEntryBudget) (out *ipEntryAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if p == nil || lifetime == nil || lifetime.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	if err = b.check(ctx, turn, 0); err != nil {
		return nil, err
	}
	tx := turn.transaction
	if tx.writer == nil || tx.writer.now == nil {
		return nil, ErrInvalid
	}
	now := tx.writer.now()
	if err = tx.observeTime(now); err != nil {
		return nil, err
	}
	if !p.tokenExpiry.After(now) || !p.horizon.After(now) {
		return nil, ErrDisclosureDenied
	}
	w, i := p.witness, p.witness.issuer
	var nativeBytes, pinGate int64
	row, e := wc008DraftQuery(ctx, turn, usAuthorityFamily, i.tokenDigest, i.family, i.familyGeneration, w.tokenExpiry, i.server, i.account, i.profile, i.epoch, i.authorityGeneration, i.memberIncarnation, i.profileLifetime, w.device, w.horizon, w.remember, w.manage, w.lineage)
	if e != nil {
		return nil, e
	}
	if e = row.Scan(&nativeBytes, &pinGate); e != nil {
		return nil, usAuthorityReadError(e)
	}
	if pinGate != 0 && pinGate != 1 {
		return nil, ErrResolveRequired
	}
	if err = b.check(ctx, turn, nativeBytes); err != nil {
		return nil, err
	}
	protected := false
	row, e = wc008DraftQuery(ctx, turn, usAuthorityPrincipal, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime)
	if e != nil {
		return nil, e
	}
	var present int64
	e = row.Scan(&present)
	if errors.Is(e, sql.ErrNoRows) {
		if err = usAuthorityPresence(ctx, turn, usAuthorityPrincipalPIN, true, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime); err != nil {
			return nil, err
		}
		protected = true
	} else if e != nil {
		return nil, e
	} else if present != 1 {
		return nil, ErrResolveRequired
	}
	for _, check := range []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usAuthorityCurrentServer, []any{i.server}, true},
		{usAuthorityState, []any{i.authorityGeneration}, true},
		{usAuthorityDevice, []any{w.device, i.account, w.installationID}, true},
		{usAuthorityInstallation, []any{p.installationHash, i.installation}, true},
		{usAuthorityGrant, []any{i.family}, false},
		{usAuthorityCredential, []any{i.family}, false},
		{usAuthorityResetSession, []any{i.family}, false},
		{usAuthorityWaiting, []any{i.account}, false},
	} {
		if err = usAuthorityPresence(ctx, turn, check.id, check.present, check.args...); err != nil {
			return nil, err
		}
	}
	if err = usAuthorityFenceCheck(ctx, turn, w, "device"); err != nil {
		return nil, err
	}
	until := p.tokenExpiry
	if p.horizon.Before(until) {
		until = p.horizon
	}
	if protected {
		if pinGate != 0 {
			return nil, ErrDisclosureDenied
		}
		if err = usAuthorityFenceCheck(ctx, turn, w, "profile_pin"); err != nil {
			return nil, err
		}
		// An explicit remembered-profile/lineage forget is a persistent PIN
		// gate. Its deny cannot disappear merely because remember_until passed.
		if w.remember != "" {
			for _, kind := range []string{"profile_remember", "profile_lineage"} {
				if err = usAuthorityFenceCheck(ctx, turn, w, kind); err != nil {
					return nil, err
				}
			}
		}
		if p.remember.After(now) {
			if !p.manage.After(now) {
				return nil, ErrDisclosureDenied
			}
			if p.manage.Before(until) {
				until = p.manage
			}
			if p.remember.Before(until) {
				until = p.remember
			}
		}
	}
	// This is the only non-test mint, after all independent current source checks.
	// It is not a command/receipt/commit or public ingress capability.
	if !until.After(tx.writer.now()) {
		return nil, ErrDisclosureDenied
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return nil, err
	}
	frozen := *p
	out = &ipEntryAuthorization{turn: turn, proof: frozen, lifetime: lifetime, until: until, budget: b}
	return out, nil
}

// The stamp is bound to one actual turn; it cannot be reused after rollback,
// COMMIT (including unknown COMMIT), on another frame, or after revocation.
type ipEntryAuthorization struct {
	turn     *commandTurn
	proof    usOwnerAuthorityProof
	lifetime context.Context
	until    time.Time
	budget   ipEntryBudget
}

func (a *ipEntryAuthorization) recheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if a == nil || a.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.done || a.lifetime == nil || a.lifetime.Err() != nil {
		return ErrDisclosureDenied
	}
	_, err = authorizeIPEntryOwner(ctx, turn, &a.proof, a.lifetime, a.budget)
	return err
}
