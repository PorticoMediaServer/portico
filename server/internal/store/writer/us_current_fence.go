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
// session for personal flags600, preparation birth700 and public reading770/771.
// It grants no item/library/reading/media-creation command,
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
	usCurrentPrimary      stmt.ID = "us.current_session.primary"
	usCurrentDevice       stmt.ID = "us.current_session.device"
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
			d.ID = usCurrentDevice
			d.SQL += " AND platform<>'api'"
		case usAuthorityPrincipal, usAuthorityPrincipalPIN:
			protected := d.ID == usAuthorityPrincipalPIN
			d.ID = usCurrentPrincipal
			if protected {
				d.ID = usCurrentPrincipalPIN
			}
			d.SQL = `SELECT m.role,p.is_primary FROM accounts AS a INDEXED BY sqlite_autoindex_accounts_1
CROSS JOIN direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 ON m.account_id=a.id
CROSS JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_2 ON p.account_id=a.id
WHERE a.id=? AND a.epoch=? AND a.must_change=0 AND m.incarnation=? AND m.disabled=0
AND p.id=? AND p.lifetime=? AND p.deleted=0 AND p.pin_hash IS NULL`
			if protected {
				d.SQL = strings.Replace(d.SQL, "p.pin_hash IS NULL", "p.pin_hash IS NOT NULL", 1)
			}
		}
		d.Owner = "store/writer US current local reading session"
		d.CallSites = []string{"store/writer/us_current_fence.go"}
	}
	defs = append(defs, stmt.Definition{ID: usCurrentPrimary, Kind: stmt.Read, Owner: "store/writer US current local session primary binding", CallSites: []string{"store/writer/us_current_fence.go"}, SQL: `SELECT 1 FROM accounts INDEXED BY sqlite_autoindex_accounts_1 WHERE id=? AND profile_id=?`, Params: []stmt.Param{{Name: "account", Type: stmt.Text}, {Name: "profile", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "current_primary", Bindings: []any{"issuer", "issuer-profile"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_accounts_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "accounts", Name: "sqlite_autoindex_accounts_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}})
	return defs
}

func authorizeUSCurrentSession(ctx context.Context, turn *commandTurn, p *usOwnerAuthorityProof, lifetime context.Context) (out *usCurrentSessionAuthorization, err error) {
	defer func() { out, err = finishCommand(ctx, lifetime, out, err) }()
	if turn == nil || turn.transaction == nil || p == nil || lifetime == nil {
		return nil, ErrDisclosureDenied
	}
	// Revalidate comparison preparation: parsed times/hash cannot be replaced
	// independently of the raw values compared to native rows.
	verified, e := prepareUSOwnerAuthority(p.witness)
	if e != nil {
		return nil, e
	}
	p = verified
	tx := turn.transaction
	if !tx.begun || tx.done || !tx.policyVerified || tx.writer == nil || tx.descriptor.Durability != Normal || tx.settings.Synchronous != int(sqliteconn.Normal) {
		return nil, ErrDisclosureDenied
	}
	switch tx.descriptor.ID {
	case SetPersonalFlags, 700, 770, 771:
	case 740:
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
	row, e = wc008DraftQuery(ctx, turn, usCurrentPrincipal, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime)
	if e != nil {
		return nil, e
	}
	protected := false
	var memberRole string
	var primary int64
	err = row.Scan(&memberRole, &primary)
	if errors.Is(err, sql.ErrNoRows) {
		row, e = wc008DraftQuery(ctx, turn, usCurrentPrincipalPIN, i.account, i.epoch, i.memberIncarnation, i.profile, i.profileLifetime)
		if e != nil {
			return nil, e
		}
		err = row.Scan(&memberRole, &primary)
		protected = true
	}
	if err != nil {
		return nil, usAuthorityReadError(err)
	}
	if primary != 0 && primary != 1 || memberRole != "owner" && memberRole != "admin" && memberRole != "member" {
		return nil, ErrDisclosureDenied
	}
	// Session role is the product's effective profile role, not an account tier:
	// secondary profiles always receive member, including owner/admin accounts.
	if primary == 0 {
		if role != "member" {
			return nil, ErrDisclosureDenied
		}
	} else {
		if role != memberRole {
			return nil, ErrDisclosureDenied
		}
		if err = usAuthorityPresence(ctx, turn, usCurrentPrimary, true, i.account, i.profile); err != nil {
			return nil, err
		}
	}

	for _, c := range []struct {
		id      stmt.ID
		args    []any
		present bool
	}{
		{usAuthorityCurrentServer, []any{i.server}, true}, {usAuthorityState, []any{i.authorityGeneration}, true},
		{usCurrentDevice, []any{w.device, i.account, w.installationID}, true}, {usAuthorityInstallation, []any{p.installationHash, i.installation}, true},
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
