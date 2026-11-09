package writer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// This closed NORMAL recipe proves only a current ordinary LOCAL interactive
// session for personal flags 600 and public reading 770/771. It grants no item/library/reading command,
// owner/admin power, MP source proof, worker/Hosted/reset/recovery authority.
// Those remain independent parent conditions. Prepared US-01 witnesses contain
// comparisons only; no US-01 owner stamp or FULL gate is widened here.
type usCurrentSessionAuthorization struct {
	turn     *commandTurn
	proof    usOwnerAuthorityProof
	lifetime context.Context
	until    time.Time
}

func (*usCurrentSessionAuthorization) Format(s fmt.State, _ rune) {
	formatUSPreparation(s, "usCurrentSessionAuthorization")
}

const (
	usCurrentFamily       stmt.ID = "us.current_session.family"
	usCurrentPrincipal    stmt.ID = "us.current_session.principal"
	usCurrentPrincipalPIN stmt.ID = "us.current_session.principal_pin"
)

func usCurrentSessionDefinitions() []stmt.Definition {
	defs := usAuthorityDefinitions()
	for n := range defs {
		d := &defs[n]
		switch d.ID {
		case usAuthorityFamily:
			d.ID = usCurrentFamily
			d.SQL = strings.Replace(d.SQL, "SELECT f.control_variable_bytes,f.pin_gate", "SELECT f.control_variable_bytes,f.pin_gate,f.role", 1)
			d.SQL = strings.Replace(d.SQL, "f.role='owner'", "f.role IN('owner','admin','member')", 1)
		case usAuthorityDevice:
			d.SQL += " AND platform<>'api'"
		case usAuthorityPrincipal, usAuthorityPrincipalPIN:
			protected := d.ID == usAuthorityPrincipalPIN
			d.ID = usCurrentPrincipal
			if protected {
				d.ID = usCurrentPrincipalPIN
			}
			d.SQL = `SELECT 1 FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.must_change=0 AND m.incarnation=? AND m.disabled=0
AND p.id=? AND p.lifetime=? AND p.deleted=0 AND m.role=? AND p.pin_hash IS NULL`
			if protected {
				d.SQL = strings.Replace(d.SQL, "p.pin_hash IS NULL", "p.pin_hash IS NOT NULL", 1)
			}
			d.Params = append(d.Params, stmt.Param{Name: "current_role", Type: stmt.Text})
			d.Cohorts[0].Bindings = append(d.Cohorts[0].Bindings, "member")
		}
		d.Owner = "store/writer US current local reading session"
		d.CallSites = []string{"store/writer/us_current_fence.go"}
	}
	return defs
}

func authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context) (out *usCurrentSessionAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || p == nil || lifetime == nil {
		return nil, ErrDisclosureDenied
	}
	tx := turn.transaction
	if !tx.begun || tx.done || !tx.policyVerified || tx.writer == nil || tx.descriptor.Durability != Normal || tx.settings.Synchronous != int(sqliteconn.Normal) {
		return nil, ErrDisclosureDenied
	}
	switch tx.descriptor.ID {
	case SetPersonalFlags, 770, 771:
	default:
		return nil, ErrDisclosureDenied
	}
	if err = turn.checkLifetimes(ctx); err != nil {
		return nil, err
	}
	now := tx.writer.now()
	if err = tx.observeTime(now); err != nil {
		return nil, err
	}
	until := p.tokenExpiry
	if p.horizon.Before(until) {
		until = p.horizon
	}
	deadline, ok := tx.ctx.Deadline()
	if !ok || !until.After(now) || deadline.After(until) {
		return nil, ErrDisclosureDenied
	}
	w, i := p.witness, p.witness.issuer
	row, e := wc008DraftQuery(ctx, turn, usCurrentFamily, i.tokenDigest, i.family, i.familyGeneration, w.tokenExpiry, i.server, i.account, i.profile, i.epoch, i.authorityGeneration, i.memberIncarnation, i.profileLifetime, w.device, w.horizon, w.remember, w.manage, w.lineage)
	if e != nil {
		return nil, e
	}
	var nativeBytes, pinGate int64
	var role string
	if e = row.Scan(&nativeBytes, &pinGate, &role); e != nil {
		return nil, usAuthorityReadError(e)
	}
	if nativeBytes < 0 || nativeBytes > maxCanonicalResultBytes || (pinGate != 0 && pinGate != 1) || (role != "owner" && role != "admin" && role != "member") {
		return nil, ErrDisclosureDenied
	}
	row, e = wc008DraftQuery(ctx, turn, usCurrentPrincipal, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime, role)
	if e != nil {
		return nil, e
	}
	protected := false
	var present int64
	err = row.Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		if err = usAuthorityPresence(ctx, turn, usCurrentPrincipalPIN, true, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime, role); err != nil {
			return nil, err
		}
		protected = true
	} else if err != nil {
		return nil, err
	} else if present != 1 {
		return nil, ErrDisclosureDenied
	}

	for _, c := range []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usAuthorityCurrentServer, []any{i.server}, true}, {usAuthorityState, []any{i.authorityGeneration}, true},
		{usAuthorityDevice, []any{w.device, i.account, w.installationID}, true}, {usAuthorityInstallation, []any{p.installationHash, i.installation}, true},
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
		if w.remember != "" {
			for _, kind := range []string{"profile_remember", "profile_lineage"} {
				if err = usAuthorityFenceCheck(ctx, turn, w, kind); err != nil {
					return nil, err
				}
			}
		}
	}
	// Ordinary reading is not profile management: do not copy US-01's owner
	// manage-until requirement. Family token/horizon and persistent PIN gates apply.
	if err = turn.checkLifetimes(ctx); err != nil {
		return nil, err
	}
	return &usCurrentSessionAuthorization{turn: turn, proof: *p, lifetime: lifetime, until: until}, nil
}

func (a *usCurrentSessionAuthorization) recheck(ctx context.Context, turn *commandTurn) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if a == nil || a.turn != turn || a.lifetime == nil || a.lifetime.Err() != nil {
		return ErrDisclosureDenied
	}
	_, err = authorizeUSCurrentSession(ctx, turn, &a.proof, a.lifetime)
	return err
}
