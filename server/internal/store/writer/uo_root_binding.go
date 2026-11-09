package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"
)

// ErrUORootGone is the single error class for a root that is not the live owner
// for the validated identity: missing, deleted, or any account/membership,
// profile lifetime, installation or family mismatch. A gone root may neither
// apply nor replay, and missing and mismatched roots are indistinguishable.
var ErrUORootGone = errors.New("writer UO root is gone")

// ErrUORootCurrentFenceUnavailable refuses a missing or unsupported NORMAL
// current-session proof. The supported set is a closed, per-purpose list
// (600 personal flags, 700 preparation birth, 710 session birth) and is not a
// callback or a generic authorizer. This is not a retry signal
// and never means the root is gone.
var ErrUORootCurrentFenceUnavailable = errors.New("writer UO root current fence unavailable")

// uoRootIdentity is the exact validated scope a retained root original must
// still match. It is filled only from a validated US-01 stamp (FULL) or its
// opaque NORMAL proof. A caller string never fills it.
type uoRootIdentity struct {
	account, memberIncarnation, profile, profileLifetime, installation, family string
}

func (i uoRootIdentity) complete() bool {
	for _, v := range []string{i.account, i.memberIncarnation, i.profile, i.profileLifetime, i.installation, i.family} {
		if v == "" || !utf8.ValidString(v) {
			return false
		}
	}
	return true
}

// uoRootCurrentProof is the opaque NORMAL current-session proof seam. Its only
// producer is the closed bindUSCurrentUORoot bridge below; callers have no
// public constructor or exported field, so no caller bool, string or cached issuer
// can stand in for it. Unsupported NORMAL purposes refuse with
// ErrUORootCurrentFenceUnavailable. The frame, purpose, lifetime, deadline
// and identity fields bind this mint and make reuse fail.
type uoRootCurrentProof struct {
	session  *usCurrentSessionAuthorization
	turn     *commandTurn
	purpose  CommandID
	lifetime context.Context
	deadline time.Time
	identity uoRootIdentity
}

func (p *uoRootCurrentProof) current(turn *commandTurn) error {
	if p == nil || turn == nil || turn.transaction == nil || turn.transaction.ctx == nil || p.turn != turn || p.lifetime == nil || p.lifetime.Err() != nil || p.purpose != turn.transaction.descriptor.ID || !turn.transaction.begun || turn.transaction.done || !p.identity.complete() {
		return ErrUORootCurrentFenceUnavailable
	}
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok || !p.deadline.Equal(deadline) {
		return ErrUORootCurrentFenceUnavailable
	}
	if p.session == nil || p.session.turn != turn || p.lifetime != p.session.lifetime || p.identity != uoRootSessionIdentity(p.session) {
		return ErrUORootCurrentFenceUnavailable
	}
	switch p.purpose {
	case 600, 700, 710:
	default:
		return ErrUORootCurrentFenceUnavailable
	}
	return p.session.recheck(turn.transaction.ctx, turn)
}

func uoRootSessionIdentity(a *usCurrentSessionAuthorization) uoRootIdentity {
	i := a.proof.witness.issuer
	return uoRootIdentity{account: i.account, memberIncarnation: i.memberIncarnation, profile: i.profile, profileLifetime: i.profileLifetime, installation: i.installation, family: i.family}
}

// Sole NORMAL bridge: a current source-checked US proof, never a caller scope.
// Root matching remains independent and is repeated by every root operation.
func bindUSCurrentUORoot(ctx context.Context, turn *commandTurn, a *usCurrentSessionAuthorization) (out *uoRootCurrentProof, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if a == nil || a.turn != turn || turn == nil || turn.transaction == nil {
		return nil, ErrUORootCurrentFenceUnavailable
	}
	switch turn.transaction.descriptor.ID {
	case 600, 700, 710:
	default:
		return nil, ErrUORootCurrentFenceUnavailable
	}
	if err = a.recheck(ctx, turn); err != nil {
		return nil, err
	}
	deadline, ok := turn.transaction.ctx.Deadline()
	if !ok {
		return nil, ErrUORootCurrentFenceUnavailable
	}
	return &uoRootCurrentProof{session: a, turn: turn, purpose: turn.transaction.descriptor.ID, lifetime: a.lifetime, deadline: deadline, identity: uoRootSessionIdentity(a)}, nil
}

// uoRootBinding is the only door from a domain command into the shared lifetime
// root's retained owner/receipt/payload custody. It mints no authority and adds
// no issuer: the FULL arm consumes the reviewed US-01 stamp unchanged, and the
// NORMAL arm can only present a private us_current_fence proof.
//
// Followers UO-02, UO-03, UO-05 and later copy exactly this rule: bind once at
// entry, check before the first retained lookup, recheck root identity/state and
// current authority at final release, and never substitute a caller bool, family
// string or frozen issuer for the proof.
type uoRootBinding struct {
	security *usOwnerAuthorization
	current  *uoRootCurrentProof
	identity uoRootIdentity
}

func (uoRootBinding) Format(s fmt.State, _ rune) { formatUSPreparation(s, "uoRootBinding") }

func bindUORoot(turn *commandTurn, security *usOwnerAuthorization, current *uoRootCurrentProof) (out uoRootBinding, err error) {
	if turn == nil || turn.transaction == nil || !turn.transaction.begun || turn.transaction.done {
		return out, ErrInvalid
	}
	descriptor := turn.transaction.descriptor
	registered, err := Describe(descriptor.ID)
	if err != nil || registered != descriptor {
		return out, ErrInvalid
	}
	switch descriptor.Durability {
	case Normal:
		// Durability does not select authority. There is no interactive owner
		// token for background delivery/outbox either; those need their own
		// reviewed lease/credential-generation proof.
		if security != nil {
			return out, ErrInvalid
		}
		if current == nil {
			return out, ErrUORootCurrentFenceUnavailable
		}
		if err := current.current(turn); err != nil {
			return out, err
		}
		return uoRootBinding{current: current, identity: current.identity}, nil
	case Full:
		if current != nil {
			return out, ErrInvalid
		}
		if security == nil || security.turn != turn {
			return out, ErrDisclosureDenied
		}
		identity := uoRootIdentity{
			account:           security.proof.witness.issuer.account,
			memberIncarnation: security.proof.witness.issuer.memberIncarnation,
			profile:           security.proof.witness.issuer.profile,
			profileLifetime:   security.proof.witness.issuer.profileLifetime,
			installation:      security.proof.witness.issuer.installation,
			family:            security.proof.witness.issuer.family,
		}
		if !identity.complete() {
			return out, ErrDisclosureDenied
		}
		return uoRootBinding{security: security, identity: identity}, nil
	default:
		return out, ErrInvalid
	}
}

func (b *uoRootBinding) check(ctx context.Context, turn *commandTurn) error {
	if b == nil || turn == nil || turn.transaction == nil || turn.transaction.done {
		return ErrDisclosureDenied
	}
	switch turn.transaction.descriptor.Durability {
	case Full:
		if b.security == nil || b.security.turn != turn {
			return ErrDisclosureDenied
		}
		// US-01 re-runs every independent current source check on this turn.
		return b.security.recheck(ctx, turn)
	case Normal:
		if b.current == nil || b.security != nil || b.identity != b.current.identity {
			return ErrUORootCurrentFenceUnavailable
		}
		if err := b.current.current(turn); err != nil {
			return err
		}
		return turn.checkLifetimes(ctx) // native current.current above already re-proved every source
	default:
		return ErrDisclosureDenied
	}
}

// root reads the native root PK before any command-owner/header/payload access
// and proves it is the live owner for this binding's validated identity. A miss
// and every identity mismatch return the same ErrUORootGone class.
func (b *uoRootBinding) root(ctx context.Context, turn *commandTurn, rootID string) (state string, err error) {
	if b == nil || turn == nil || !b.identity.complete() || len(rootID) != 22 {
		return "", ErrUORootGone
	}
	row, err := wc008DraftQuery(ctx, turn, uoRootIdentityPoint, rootID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrUORootGone
	}
	if err != nil {
		return "", err
	}
	var account, profile, accountIncarnation, profileIncarnation, family, installation, rootState string
	if err = row.Scan(&account, &profile, &accountIncarnation, &profileIncarnation, &family, &installation, &rootState); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrUORootGone
		}
		return "", err
	}
	if account != b.identity.account || profile != b.identity.profile || accountIncarnation != b.identity.memberIncarnation || profileIncarnation != b.identity.profileLifetime || family != b.identity.family || installation != b.identity.installation || (rootState != "active" && rootState != "draining") {
		return "", ErrUORootGone
	}
	return rootState, nil
}
