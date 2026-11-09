package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This proof is only current LOCAL ordinary interactive owner/admin management
// on the account's main profile, inside one approved 550/551/552 FULL turn.
// It grants no target tier, Hosted/setup/reset/recovery/account-scoped authority,
// command execution or retained-result disclosure by itself. US-13 checks the
// current target/requested/disclosed tiers, issuer, classified original and its
// additional source conditions, then returns the final Recheck error instead of
// any body outcome. No production route is wired by this private component.
type usInvitationAuthorization struct {
	turn            *commandTurn
	purpose         CommandID
	proof           usOwnerAuthorityProof
	issuer          usPolicyIssuer
	role            string
	lifetime        context.Context
	deadline, until time.Time
	budget          usInvitationAuthorityBudget
	seal            *usInvitationAuthorization
	refused         bool
}

func (usInvitationAuthorization) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationAuthorization")
}

// The memory reservation belongs to the parent's funded frame. This is not a
// second governor, a public issuer or a descriptor-based admission bypass.
type usInvitationAuthorityBudget struct {
	memory  uint64
	fixture *usOwnerAuthorityFixturePermit
}

func (usInvitationAuthorityBudget) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usInvitationAuthorityBudget")
}

func (b usInvitationAuthorityBudget) check(ctx context.Context, turn *commandTurn, bytes int64) error {
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	tx := turn.transaction
	registered, err := Describe(tx.descriptor.ID)
	if err != nil || registered != tx.descriptor || !tx.begun || tx.done || !tx.policyVerified || tx.descriptor.Durability != Full || tx.settings.Synchronous != int(sqliteconn.Full) {
		return ErrInvalid
	}
	switch tx.descriptor.ID {
	case usInvitationCreate, usInvitationEdit, usInvitationRevoke:
	default:
		return ErrInvalid
	}
	if bytes < 0 || bytes > maxCanonicalResultBytes || uint64(64<<10)+uint64(bytes)*32 > b.memory {
		return &Refusal{Code: RefusalCapacity}
	}
	if g, ok := tx.custody.(*governedTurn); ok {
		if err := g.check(tx.descriptor.ID, tx.workLimit); err != nil {
			return err
		}
		if g.expected.Need[governor.MemoryBytes] <= 0 || b.memory > uint64(g.expected.Need[governor.MemoryBytes]) {
			return &Refusal{Code: RefusalUnqualified}
		}
	} else if b.fixture == nil || b.fixture.frame != tx {
		return &Refusal{Code: RefusalUnqualified}
	}
	return turn.checkLifetimes(ctx)
}

// This is the only mint. The US-01 preparation holds owned comparisons only;
// parsed times/hash are re-derived from raw witness values before native use.
// No cached DTO/role, callback, public constructor or caller bool mints a proof.
// Wiring: the Fresh production entry passes prepared.scope.issuer as expected;
// it is compared with the independently current session witness, never inferred
// from Issuer() alone. Resolve/ACK have no Fresh prepared request and need their
// own current issuer binding source in us_invitation_receipts.go, including the
// native security-owner lifetime columns. Retained originals are not a current
// binding source, and their original request digest must not be rewritten.
func authorizeUSInvitationManager(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, expected usPolicyIssuer, lifetime context.Context, b usInvitationAuthorityBudget) (out *usInvitationAuthorization, err error) {
	defer func() { usInvitationAuthorityFailure(turn, err) }()
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if p == nil || lifetime == nil || lifetime.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	if expected != p.witness.issuer {
		return nil, ErrDisclosureDenied
	}
	if err = b.check(ctx, turn, 0); err != nil {
		return nil, err
	}
	verified, e := prepareUSOwnerAuthority(p.witness)
	if e != nil {
		return nil, e
	}
	p = verified
	tx := turn.transaction
	if tx.writer == nil || tx.writer.now == nil {
		return nil, ErrInvalid
	}
	now := tx.writer.now()
	if err = tx.observeTime(now); err != nil {
		return nil, err
	}
	w, i := p.witness, p.witness.issuer
	until := p.tokenExpiry
	if p.horizon.Before(until) {
		until = p.horizon
	}
	if !until.After(now) {
		return nil, ErrDisclosureDenied
	}
	row, e := wc008DraftQuery(ctx, turn, usInvitationAuthorityFamily, i.tokenDigest, i.family, i.familyGeneration, w.tokenExpiry, i.server, i.account, i.profile, i.epoch, i.authorityGeneration, i.memberIncarnation, i.profileLifetime, w.device, w.horizon, w.remember, w.manage, w.lineage)
	if e != nil {
		return nil, e
	}
	var nativeBytes, pinGate int64
	var role string
	if e = row.Scan(&nativeBytes, &pinGate, &role); e != nil {
		return nil, usAuthorityReadError(e)
	}
	if (role != "owner" && role != "admin") || (pinGate != 0 && pinGate != 1) {
		return nil, ErrDisclosureDenied
	}
	if err = b.check(ctx, turn, nativeBytes); err != nil {
		return nil, err
	}
	row, e = wc008DraftQuery(ctx, turn, usInvitationAuthorityPrincipal, i.account, i.epoch, i.memberIncarnation, role, i.profile, i.profileLifetime)
	if e != nil {
		return nil, e
	}
	var present int64
	protected := false
	e = row.Scan(&present)
	if errors.Is(e, sql.ErrNoRows) {
		if err = usAuthorityPresence(ctx, turn, usInvitationAuthorityPrincipalPIN, true, i.account, i.epoch, i.memberIncarnation, role, i.profile, i.profileLifetime); err != nil {
			return nil, err
		}
		protected = true
	} else if e != nil {
		return nil, e
	} else if present != 1 {
		return nil, ErrDisclosureDenied
	}
	for _, c := range []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usAuthorityCurrentServer, []any{i.server}, true}, {usAuthorityState, []any{i.authorityGeneration}, true},
		{usInvitationAuthorityDevice, []any{w.device, i.account, w.installationID}, true}, {usAuthorityInstallation, []any{p.installationHash, i.installation}, true},
		{usAuthorityGrant, []any{i.family}, false}, {usAuthorityCredential, []any{i.family}, false}, {usAuthorityResetSession, []any{i.family}, false}, {usAuthorityWaiting, []any{i.account}, false},
	} {
		if err = usAuthorityPresence(ctx, turn, c.id, c.present, c.args...); err != nil {
			return nil, err
		}
	}
	if err = usAuthorityFenceCheck(ctx, turn, w, "device"); err != nil {
		return nil, err
	}
	if protected {
		if pinGate != 0 {
			return nil, ErrDisclosureDenied
		}
		if err = usAuthorityFenceCheck(ctx, turn, w, "profile_pin"); err != nil {
			return nil, err
		}
		// A lineage/remember forget remains a gate after remember_until expires.
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
	deadline, ok := tx.ctx.Deadline()
	if !ok || deadline.After(until) || !until.After(tx.writer.now()) {
		return nil, ErrDisclosureDenied
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return nil, err
	}
	out = &usInvitationAuthorization{turn: turn, purpose: tx.descriptor.ID, proof: *p, issuer: expected, role: role, lifetime: lifetime, deadline: deadline, until: until, budget: b}
	out.seal = out
	return out, nil
}

func (a *usInvitationAuthorization) usable(turn *commandTurn) bool {
	if a == nil || a.seal != a || a.refused || turn == nil || a.turn != turn || turn.transaction == nil {
		return false
	}
	tx := turn.transaction
	if !tx.begun || tx.done || tx.ctx == nil || tx.ctx.Err() != nil || a.lifetime == nil || a.lifetime.Err() != nil || tx.writer == nil || tx.writer.now == nil || tx.descriptor.ID != a.purpose || tx.descriptor.Durability != Full || !tx.policyVerified || tx.settings.Synchronous != int(sqliteconn.Full) {
		return false
	}
	switch a.purpose {
	case usInvitationCreate, usInvitationEdit, usInvitationRevoke:
	default:
		return false
	}
	registered, err := Describe(a.purpose)
	if err != nil || registered != tx.descriptor {
		return false
	}
	deadline, ok := tx.ctx.Deadline()
	return ok && deadline.Equal(a.deadline) && !deadline.After(a.until) && a.until.After(tx.writer.now()) && (a.role == "owner" || a.role == "admin")
}

// Issuer and ManagesTier are comparisons AFTER a successful current Recheck.
// They never replace that native re-proof at entry/disclosure/final release.
func (a *usInvitationAuthorization) Issuer() string {
	if a == nil || !a.usable(a.turn) {
		return ""
	}
	return a.issuer.account
}
func (a *usInvitationAuthorization) ManagesTier(role string) bool {
	if a == nil || !a.usable(a.turn) || (role != "member" && role != "admin") {
		return false
	}
	return a.role == "owner" || role == "member"
}

// A native source refusal cancels the frame in the nested mint. Its denial can
// therefore match BOTH ErrDisclosureDenied and context.Canceled. Adapters must
// classify denial FIRST and replace the body outcome; cancellation must not turn
// an authority refusal into a transport retry.
func (a *usInvitationAuthorization) Recheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() {
		if err != nil && a != nil {
			a.refused = true
		}
		usInvitationAuthorityFailure(turn, err)
	}()
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !a.usable(turn) {
		return ErrDisclosureDenied
	}
	next, e := authorizeUSInvitationManager(ctx, turn, &a.proof, a.issuer, a.lifetime, a.budget)
	if e != nil {
		return e
	}
	// A live tier change cannot refresh an old proof's cached permissions.
	if next.role != a.role {
		return ErrDisclosureDenied
	}
	return nil
}

// A refused authority entry cannot leave a composed prefix committable if its
// caller ignores the error. The physical terminal observes the canceled frame.
func usInvitationAuthorityFailure(turn *commandTurn, err error) {
	if err != nil && turn != nil && turn.transaction != nil && turn.transaction.begun && !turn.transaction.done && turn.transaction.cancel != nil {
		turn.transaction.cancel()
	}
}
