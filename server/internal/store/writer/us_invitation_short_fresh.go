package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usInvitationShortKind stmt.ID = "us.invitation.short.kind.current"
	usInvitationShortRead stmt.ID = "us.invitation.short.source.current"
)

// Private definitions only; adoption and the closed production plan belong to W.
func usInvitationShortFreshDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: usInvitationShortKind, SQL: `SELECT 1 FROM identity_state WHERE singleton=1`, Cohorts: []stmt.Cohort{{Name: "point"}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"identity_state"}, Seeks: []string{"rowid=?"}}},
		{ID: usInvitationShortRead, SQL: `SELECT ` + usInvitationShortSourceColumns + ` FROM access_invitation_short_codes INDEXED BY sqlite_autoindex_access_invitation_short_codes_1 WHERE invitation_id=?`, Params: []stmt.Param{{Name: "invitation", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "point", Bindings: []any{"invite"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_access_invitation_short_codes_1"}, Seeks: []string{"invitation_id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "access_invitation_short_codes", Name: "sqlite_autoindex_access_invitation_short_codes_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "invitation_id", Collation: "BINARY"}}}}}},
	}
	for n := range defs {
		defs[n].Kind = stmt.Read
		defs[n].Owner = "store/writer US short fresh"
		defs[n].CallSites = []string{"store/writer/us_invitation_short_fresh.go"}
	}
	d := usInvitationSourceDefinitions()[0]
	d.ID = "us.invitation.short.invitation.current"
	d.SQL = `SELECT 1 FROM access_invitations AS i INDEXED BY sqlite_autoindex_access_invitations_1 WHERE i.id=? AND i.revision=? AND i.code_hash=? AND i.role=? AND i.expires_ms=? AND i.state='pending' AND i.accepted_ms IS NULL AND i.account_id IS NULL`
	d.Params = []stmt.Param{{Name: "invitation", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}, {Name: "hash", Type: stmt.Text}, {Name: "role", Type: stmt.Text}, {Name: "expires", Type: stmt.Integer}}
	d.Cohorts = []stmt.Cohort{{Name: "point", Bindings: []any{"invite", int64(1), usInvitationFixtureHash, "member", int64(2000)}}}
	d.CallSites = []string{"store/writer/us_invitation_short_fresh.go"}
	return append(defs, d)
}

// Fresh-only prefix: the caller must classify the immutable receipt first.
// This consumes the same private current Localowner authority; it issues none.
// No receipt, event, charging, revision increment or publication occurs here.
func checkUSInvitationShortFresh(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, c *usInvitationShortCommand, now int64) (oldBytes int64, err error) {
	defer func() {
		oldBytes, err = finishCommand(ctx, commandLifetime(turn), oldBytes, errors.Join(err, a.check(turn)))
	}()
	if err = checkUSSecurityDisclosure(ctx, turn, a); err != nil {
		return 0, err
	}
	if _, err = c.checkedDigest(); err != nil {
		return 0, err
	}
	p := c.request
	q := p.original
	if q.scope.issuer != a.issuer {
		return 0, ErrConflict
	}
	if now < q.issuedMS || now >= p.expiresMS {
		return 0, ErrConflict
	}
	if err = usAuthorityPresence(ctx, turn, "us.invitation.short.invitation.current", true, q.invitationID, q.invitationRevision, q.codeHash, q.role, q.invitationExpiryMS); err != nil {
		return 0, err
	}
	row, e := usPolicyRow(ctx, turn, usInvitationShortKind)
	if e != nil {
		return 0, e
	}
	var present int64
	if err = row.Scan(&present); errors.Is(err, sql.ErrNoRows) {
		if q.signInKind != "portico" || q.kindRevision != 0 {
			return 0, ErrConflict
		}
		err = nil
	} else if err != nil {
		return 0, err
	} else {
		if present != 1 {
			return 0, ErrResolveRequired
		}
		if err = usAuthorityPresence(ctx, turn, usInvitationLinkKind, true, q.signInKind, q.kindRevision); err != nil {
			return 0, err
		}
	}
	// Unlike long-link Portico mode, short capabilities always bind the actual key.
	if err = usAuthorityPresence(ctx, turn, usInvitationLinkKey, true, q.serverID, q.publicKey, q.keyIncarnation, q.resetGeneration); err != nil {
		return 0, err
	}
	return readUSInvitationShortReplacementBytes(ctx, turn, a, q.invitationID)
}

// Caller reserves the complete result plus actual replacement bytes before this
// prefix. Recheck immediately before writing; a stale preflight cannot authorize it.
func applyUSInvitationShort(ctx context.Context, turn *commandTurn, a *usPolicyAuthorization, c *usInvitationShortCommand, oldBytes int64) (err error) {
	defer func() {
		if err == nil {
			err = checkUSSecurityDisclosure(ctx, turn, a)
		}
		_, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, errors.Join(err, a.check(turn)))
	}()
	now, e := usPolicyTimeMS(ctx, turn, a)
	if e != nil {
		return e
	}
	old, e := checkUSInvitationShortFresh(ctx, turn, a, c, now)
	if e != nil {
		return e
	}
	if old != oldBytes {
		return ErrConflict
	}
	p := c.request
	q := p.original
	// RETURNING contains only bound variable values; reserve before loading them.
	n := int64(len(q.invitationID) + len(p.lookupHash) + len(p.bindingHash) + len(q.serverID) + len(q.signInKind) + len("accept"))
	if err = a.requireMemory(ctx, turn, n); err != nil {
		return err
	}
	id := usInvitationShortInsert
	if old > 0 {
		id = usInvitationShortUpdate
	}
	args := []any{p.lookupHash, p.bindingHash, q.serverID, q.invitationRevision, q.signInKind, q.kindRevision, "accept", p.expiresMS, q.invitationID}
	row, e := usPolicyRow(ctx, turn, id, args...)
	if e != nil {
		return e
	}
	if err = verifyUSInvitationShortRow(row, p); err != nil {
		return err
	}
	// Independently preflight and read the stored effect, including trigger effects.
	if _, err = readUSInvitationShortReplacementBytes(ctx, turn, a, q.invitationID); err != nil {
		return err
	}
	row, e = usPolicyRow(ctx, turn, usInvitationShortRead, q.invitationID)
	if e != nil {
		return e
	}
	if err = verifyUSInvitationShortRow(row, p); err != nil {
		return err
	}
	return usAuthorityPresence(ctx, turn, "us.invitation.short.invitation.current", true, q.invitationID, q.invitationRevision, q.codeHash, q.role, q.invitationExpiryMS)
}

func verifyUSInvitationShortRow(row interface{ Scan(...any) error }, p usInvitationShortPayload) error {
	var invitation, lookup, binding, server, kind, purpose string
	var revision, kindRevision, expires int64
	if err := row.Scan(&invitation, &lookup, &binding, &server, &revision, &kind, &kindRevision, &purpose, &expires); err != nil {
		return errors.Join(ErrResolveRequired, err)
	}
	q := p.original
	if invitation != q.invitationID || lookup != p.lookupHash || binding != p.bindingHash || server != q.serverID || revision != q.invitationRevision || kind != q.signInKind || kindRevision != q.kindRevision || purpose != "accept" || expires != p.expiresMS {
		return ErrResolveRequired
	}
	return nil
}
