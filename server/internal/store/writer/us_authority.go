package writer

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Witnesses are bounded, owned comparison inputs, not permissions. This first
// recipe admits only a direct OWNER main-profile session with a registered
// installation/device. API credentials, account-scoped/factor/setup/Hosted and
// other role recipes remain unavailable here until separately implemented.
// No public adapter uses this type. Raw installation proof is never a receipt,
// event, error or request-digest field.
type usOwnerAuthorityWitness struct {
	issuer                                 usPolicyIssuer
	device, lineage, installationID        string
	tokenExpiry, horizon, remember, manage string
}
type usOwnerAuthorityProof struct {
	witness                                usOwnerAuthorityWitness
	installationHash                       string
	tokenExpiry, horizon, remember, manage time.Time
}

func prepareUSOwnerAuthority(in usOwnerAuthorityWitness) (*usOwnerAuthorityProof, error) {
	i := in.issuer
	if !usIdentifier(i.server) || !usIdentifier(i.account) || !usIdentifier(i.profile) || !usIdentifier(i.family) || !usHex(i.memberIncarnation, 32) || !usHex(i.profileLifetime, 32) || !usHex(i.installation, 32) || !usIdentityDigest(i.tokenDigest) || i.epoch <= 0 || i.familyGeneration <= 0 || i.authorityGeneration <= 0 || !usIdentifier(in.device) || (!usIdentifier(in.installationID) || len(in.installationID) < 32) || (in.lineage != "" && !usIdentifier(in.lineage)) {
		return nil, invalid("us_authority_witness")
	}
	p := &usOwnerAuthorityProof{witness: in}
	for _, item := range []struct {
		raw      string
		optional bool
		dst      *time.Time
	}{{in.tokenExpiry, false, &p.tokenExpiry}, {in.horizon, false, &p.horizon}, {in.remember, true, &p.remember}, {in.manage, true, &p.manage}} {
		if item.optional && item.raw == "" {
			continue
		}
		// Existing issuance writes UTC whole seconds. This narrow recipe is not a
		// new product date policy; unsupported encodings need their own recipe.
		if len(item.raw) != 20 {
			return nil, &Refusal{Code: RefusalUnqualified}
		}
		parsed, err := time.Parse(time.RFC3339, item.raw)
		if err != nil || parsed.UTC().Format(time.RFC3339) != item.raw {
			return nil, invalid("us_authority_time")
		}
		*item.dst = parsed
	}
	hash := sha256.Sum256([]byte(in.installationID))
	p.installationHash = hex.EncodeToString(hash[:])
	return p, nil
}

// Values come from the parent's SAME governor/receipt admission. This is no
// second ledger or caller-selected quota. The synthetic permit exists only in
// native fixtures; a descriptor never qualifies a production recipe.
type usOwnerAuthorityBudget struct {
	memory  uint64
	fixture *usOwnerAuthorityFixturePermit
}

func (b usOwnerAuthorityBudget) check(ctx context.Context, turn *commandTurn, bytes int64) error {
	if turn == nil || turn.transaction == nil {
		return ErrInvalid
	}
	tx := turn.transaction
	if !tx.begun || tx.done || !tx.policyVerified || tx.settings.Synchronous != int(sqliteconn.Full) || tx.descriptor.Durability != Full || (tx.descriptor.ID < 500 || tx.descriptor.ID > 599) {
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

// Current source checks run before ANY retained owner/header/payload lookup.
// The native statements copy no variable source fields: exact timestamp/scope
// comparisons use frozen bindings, while current roles/gates come from source.
func authorizeUSOwnerPolicy(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context, b usOwnerAuthorityBudget) (out *usOwnerAuthorization, err error) {
	defer func() {
		_, err = finishCommand(ctx, lifetime, struct{}{}, err)
		if err != nil {
			out = nil
		}
	}()
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
			if !p.manage.After(now) || w.lineage == "" {
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
	out = &usOwnerAuthorization{turn: turn, proof: frozen, lifetime: lifetime, until: until, budget: b}
	return out, nil
}
func usAuthorityReadError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrDisclosureDenied
	}
	return err
}
func usAuthorityPresence(ctx context.Context, turn *commandTurn, id stmt.ID, present bool, args ...any) error {
	row, err := wc008DraftQuery(ctx, turn, id, args...)
	if err != nil {
		return err
	}
	var value int64
	err = row.Scan(&value)
	if !present && errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return usAuthorityReadError(err)
	}
	if !present || value != 1 {
		return ErrDisclosureDenied
	}
	return turn.checkLifetimes(ctx)
}
func usAuthorityFenceCheck(ctx context.Context, turn *commandTurn, w usOwnerAuthorityWitness, kind string) error {
	profile, lifetime, device, lineage := "", "", "", ""
	switch kind {
	case "device":
		device = w.device
	case "profile_pin", "profile_remember":
		profile, lifetime = w.issuer.profile, w.issuer.profileLifetime
	case "profile_lineage":
		profile, lifetime, lineage = w.issuer.profile, w.issuer.profileLifetime, w.lineage
	default:
		return ErrInvalid
	}
	return usAuthorityPresence(ctx, turn, usAuthorityFence, true, w.issuer.family, kind, w.issuer.account, w.issuer.memberIncarnation, profile, lifetime, device, lineage)
}

// Private fixture permit has no production constructor. A production caller
// must use the parent's actual governed turn and its funded memory reservation.
type usOwnerAuthorityFixturePermit struct{ frame *transaction }

// A stamp is valid only in its original FULL turn and principal lifetime.
// Receipt consumers must recheck immediately before disclosure/ACK/release;
// source checks are repeated so a composed prefix cannot stale the stamp.
type usOwnerAuthorization struct {
	turn     *commandTurn
	proof    usOwnerAuthorityProof
	lifetime context.Context
	until    time.Time
	budget   usOwnerAuthorityBudget
}

func (a *usOwnerAuthorization) recheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if a == nil || a.turn != turn || turn == nil || turn.transaction == nil || turn.transaction.done || a.lifetime == nil || a.lifetime.Err() != nil {
		return ErrDisclosureDenied
	}
	if !a.until.After(turn.transaction.writer.now()) {
		return ErrDisclosureDenied
	}
	_, err = authorizeUSOwnerPolicy(ctx, turn, &a.proof, a.lifetime, a.budget)
	return err
}
