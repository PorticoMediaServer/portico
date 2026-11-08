package writer

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

// Comparison witnesses are not authority. This closed fork of US's immutable
// direct-interactive recipe adds only an ordinary viewer branch. Owner-only
// configuration still requires the exact US owner/main-profile native checks.
// Hosted, grants, credentials, reset sessions and pending identity moves remain
// unqualified. Programme/rating, recording claim and allocation authority are
// extra per-command checks, not implied by this source stamp.
type mgActorKind uint8

const (
	mgActorOwner mgActorKind = iota + 1
	mgActorViewer
)

type mgActorProof struct {
	native usOwnerAuthorityProof
	kind   mgActorKind
}
type mgActorAuthorization struct {
	self     *mgActorAuthorization
	purpose  CommandID
	turn     *commandTurn
	proof    mgActorProof
	lifetime context.Context
	until    time.Time
	budget   mgAuthorityBudget
}

// Funded from the accepting frame's ONE governor reservation. No descriptor,
// private test permit or previously FULL connection qualifies a runtime recipe.
type mgAuthorityBudget struct {
	memory  uint64
	fixture *mgAuthorityFixturePermit
}
type mgAuthorityFixturePermit struct{ frame *transaction }

func (b mgAuthorityBudget) check(ctx context.Context, t *commandTurn, bytes int64) error {
	if t == nil || t.transaction == nil {
		return ErrInvalid
	}
	tx := t.transaction
	if !tx.begun || tx.done || !tx.policyVerified {
		return ErrInvalid
	}
	expected := Normal
	switch tx.descriptor.ID {
	case MGConfigureTuner:
	case MGSetSourceEnabled, MGRetireSource, MGSetSourceViewerAccess:
		expected = Full
	// Acquisition/renewal only: cleanup/reconcile/fail/delete IDs are excluded.
	case 814, 815, 820, 830, 831, 832, 833, 835:
	default:
		return ErrInvalid
	}
	native := int(sqliteconn.Normal)
	if expected == Full {
		native = int(sqliteconn.Full)
	}
	if tx.descriptor.Durability != expected || tx.settings.Synchronous != native {
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
	return t.checkLifetimes(ctx)
}

func authorizeMGActor(ctx context.Context, turn *commandTurn, proof *mgActorProof, lifetime context.Context, b mgAuthorityBudget) (out *mgActorAuthorization, err error) {
	defer func() {
		out, err = finishCommand(ctx, lifetime, out, err)
		out, err = finishCommand(ctx, commandLifetime(turn), out, err)
	}()
	if proof == nil || lifetime == nil || lifetime.Err() != nil {
		return nil, ErrDisclosureDenied
	}
	if err = b.check(ctx, turn, 0); err != nil {
		return nil, err
	}
	frozen := *proof
	// Cached parsed timestamps and installation hashes are comparison data,
	// never authority. Rebuild after admission so a forged proof cannot pair an
	// expired native string with a future cached deadline.
	p, prepareErr := prepareUSOwnerAuthority(frozen.native.witness)
	if prepareErr != nil || *p != frozen.native {
		return nil, ErrDisclosureDenied
	}
	family, principal, principalPIN := usAuthorityFamily, usAuthorityPrincipal, usAuthorityPrincipalPIN
	switch frozen.kind {
	case mgActorOwner:
	case mgActorViewer:
		family, principal, principalPIN = mgAuthorityViewerFamily, mgAuthorityViewerPrincipal, mgAuthorityViewerPIN
	default:
		return nil, ErrDisclosureDenied
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
	row, e := wc008DraftQuery(ctx, turn, family, i.tokenDigest, i.family, i.familyGeneration, w.tokenExpiry, i.server, i.account, i.profile, i.epoch, i.authorityGeneration, i.memberIncarnation, i.profileLifetime, w.device, w.horizon, w.remember, w.manage, w.lineage)
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
	row, e = wc008DraftQuery(ctx, turn, principal, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime)
	if e != nil {
		return nil, e
	}
	var present int64
	e = row.Scan(&present)
	if errors.Is(e, sql.ErrNoRows) {
		if err = usAuthorityPresence(ctx, turn, principalPIN, true, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime); err != nil {
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
	if err = mgAuthorityFenceCheck(ctx, turn, w, "device"); err != nil {
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
		if err = mgAuthorityFenceCheck(ctx, turn, w, "profile_pin"); err != nil {
			return nil, err
		}
		// An explicit remembered-profile/lineage forget is a persistent PIN
		// gate. Its deny cannot disappear merely because remember_until passed.
		if w.remember != "" {
			for _, kind := range []string{"profile_remember", "profile_lineage"} {
				if err = mgAuthorityFenceCheck(ctx, turn, w, kind); err != nil {
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
	out = &mgActorAuthorization{purpose: tID(turn), turn: turn, proof: frozen, lifetime: lifetime, until: until, budget: b}
	out.self = out
	return out, nil
}
func (a *mgActorAuthorization) recheck(ctx context.Context, t *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(t), struct{}{}, err) }()
	if a == nil || a.self != a || a.turn != t || a.purpose != tID(t) || t == nil || t.transaction == nil || a.lifetime == nil || a.lifetime.Err() != nil || !a.until.After(t.transaction.writer.now()) {
		return ErrDisclosureDenied
	}
	_, err = authorizeMGActor(ctx, t, &a.proof, a.lifetime, a.budget)
	return err
}
