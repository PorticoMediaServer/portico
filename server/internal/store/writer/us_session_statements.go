package writer

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	usSessionDeviceCheck              stmt.ID = "us.session.device.check"
	usSessionFamilyAdvance            stmt.ID = "us.session.family.advance"
	usSessionRenewalRetire            stmt.ID = "us.session.renewal.retire_access"
	usSessionRefreshRetire            stmt.ID = "us.session.refresh.retire_access"
	usSessionAccessInsert             stmt.ID = "us.session.access.insert"
	usSessionRenewalInsert            stmt.ID = "us.session.renewal.insert"
	usSessionRefreshRotate            stmt.ID = "us.session.refresh.rotate"
	usSessionRefreshReceiptDelete     stmt.ID = "us.session.refresh.receipt.delete_one"
	usSessionRefreshReceiptInsert     stmt.ID = "us.session.refresh.receipt.insert"
	usSessionRefreshPredecessorInsert stmt.ID = "us.session.refresh.predecessor.insert"
)

// Inert HN inventory. Each write affects at most ONE row, with no family fanout,
// unbounded cleanup, opener, receipt classification or accepting registration.
// Native source checks are CAS witnesses, not the parent's full current-authority
// policy. The parent must budget FK/index/binding/RETURNING work before custody.
func usSessionDefinitions() []stmt.Definition {
	text := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	integer := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	blob := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	point := func(table, key string) stmt.Plan {
		name := "sqlite_autoindex_" + table + "_1"
		return stmt.Plan{Hot: true, Indexes: []string{name}, Seeks: []string{key + "=?"},
			AutoIndexes: []stmt.AutoIndexBinding{{Table: table, Name: name, Origin: "pk", Columns: []stmt.IndexColumn{{Name: key, Collation: "BINARY"}}}}}
	}
	defs := []stmt.Definition{
		{ID: usSessionDeviceCheck, SQL: `SELECT revision FROM identity_devices INDEXED BY sqlite_autoindex_identity_devices_1 WHERE id=? AND authority='local' AND account_id=? AND installation_id=?`, Params: []stmt.Param{text("device"), text("account"), text("installation")}, Plan: point("identity_devices", "id")},
		{ID: usSessionFamilyAdvance, SQL: `UPDATE authorization_session_families INDEXED BY sqlite_autoindex_authorization_session_families_1 SET current_generation=?,authorization_horizon=?
WHERE id=? AND server_id=? AND account_id=? AND profile_id=? AND authority='local'
AND epoch=? AND current_generation=? AND authority_generation=?
AND command_member_incarnation=? AND command_profile_lifetime=? AND command_incarnation=?
AND authorization_horizon=? AND authorization_horizon>? AND revoked=0 AND device_id=?
RETURNING id`,
			Params: []stmt.Param{integer("next_generation"), text("next_horizon"), text("family"), text("server"), text("account"), text("profile"), integer("epoch"), integer("generation"), integer("authority_generation"), text("member_incarnation"), text("profile_lifetime"), blob("family_incarnation"), text("old_horizon"), text("now"), text("device")},
			Plan:   point("authorization_session_families", "id")},
		{ID: usSessionRenewalRetire, SQL: `UPDATE authorization_family_tokens INDEXED BY sqlite_autoindex_authorization_family_tokens_1 SET retired=1
WHERE token_hash=? AND family_id=? AND generation=? AND expires_at=? AND retired=0 AND expires_at>?
RETURNING family_id`,
			Params: []stmt.Param{text("predecessor"), text("family"), integer("generation"), text("old_expiry"), text("now")}, Plan: point("authorization_family_tokens", "token_hash")},
		{ID: usSessionRefreshRetire, SQL: `UPDATE authorization_family_tokens INDEXED BY sqlite_autoindex_authorization_family_tokens_1 SET retired=1
WHERE token_hash=? AND family_id=? AND generation=? AND expires_at=? AND retired=0
RETURNING family_id`,
			Params: []stmt.Param{text("predecessor"), text("family"), integer("generation"), text("old_expiry")}, Plan: point("authorization_family_tokens", "token_hash")},
		{ID: usSessionAccessInsert, SQL: `INSERT INTO authorization_family_tokens(token_hash,family_id,generation,expires_at,retired) VALUES(?,?,?,?,?) RETURNING family_id`,
			Params: []stmt.Param{text("successor"), text("family"), integer("generation"), text("expiry"), integer("retired")}, Plan: stmt.Plan{Hot: true}},
		{ID: usSessionRenewalInsert, SQL: `INSERT INTO authorization_family_renewals(family_id,request_id,predecessor_hash,controller_id,controller_epoch,result_generation,expires_at,authorization_horizon,nonce,ciphertext) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING family_id`,
			Params: []stmt.Param{text("family"), text("request"), text("predecessor"), text("controller"), text("controller_epoch"), integer("generation"), text("expiry"), text("horizon"), blob("nonce"), blob("ciphertext")}, Plan: stmt.Plan{Hot: true}},
		{ID: usSessionRefreshRotate, SQL: `UPDATE identity_refresh_credentials INDEXED BY sqlite_autoindex_identity_refresh_credentials_1 SET token_hash=?,generation=?,expires_at=?
WHERE family_id=? AND device_id=? AND token_hash=? AND generation=? AND expires_at=? AND expires_at>?
RETURNING family_id`,
			Params: []stmt.Param{text("successor"), integer("generation"), text("horizon"), text("family"), text("device"), text("predecessor"), integer("old_generation"), text("old_expiry"), text("now")}, Plan: point("identity_refresh_credentials", "family_id")},
		{ID: usSessionRefreshReceiptDelete, SQL: `DELETE FROM identity_refresh_receipts INDEXED BY sqlite_autoindex_identity_refresh_receipts_1 WHERE predecessor_hash=? AND family_id=? RETURNING family_id`,
			Params: []stmt.Param{text("predecessor"), text("family")}, Plan: point("identity_refresh_receipts", "predecessor_hash")},
		{ID: usSessionRefreshReceiptInsert, SQL: `INSERT INTO identity_refresh_receipts(predecessor_hash,family_id,request_id,ciphertext,expires_at) VALUES(?,?,?,?,?) RETURNING family_id`,
			Params: []stmt.Param{text("predecessor"), text("family"), text("request"), blob("ciphertext"), text("horizon")}, Plan: stmt.Plan{Hot: true}},
		{ID: usSessionRefreshPredecessorInsert, SQL: `INSERT INTO identity_refresh_predecessors(predecessor_hash,family_id) VALUES(?,?) RETURNING family_id`,
			Params: []stmt.Param{text("predecessor"), text("family")}, Plan: stmt.Plan{Hot: true}},
	}
	// Closed, synthetic Capture bindings. They do not represent an issuer. Real
	// execution always obtains bindings from a privately frozen rotation below.
	pins := usSessionPins{server: "us-server", account: "us-account", profile: "us-profile", family: "us-family", device: "us-device", installation: strings.Repeat("i", 32), memberIncarnation: strings.Repeat("a", 32), profileLifetime: strings.Repeat("b", 32), incarnation: [16]byte{1}, epoch: 7, authorityGeneration: 2, generation: 3, accessDigest: strings.Repeat("c", 64), accessExpiresAt: "2026-11-07T12:05:00Z", horizon: "2026-11-08T12:00:00Z"}
	sample := &usSessionRotation{kind: usSessionRenewal, clocks: usSessionClocks{now: "2026-11-07T12:00:00Z", resultHorizon: pins.horizon}, request: usSessionRotationRequest{ownerID: "us-owner", requestID: "us-request", pins: pins, controllerID: "us-controller", controllerEpoch: "us-controller-epoch", output: usSessionPreparedOutput{accessDigest: strings.Repeat("d", 64), accessExpiresAt: "2026-11-07T12:15:00Z", horizon: pins.horizon, nonce: []byte{1}, ciphertext: []byte{2}}}}
	for n := range defs {
		defs[n].Owner, defs[n].Kind = "store/writer US session components", stmt.Write
		defs[n].CallSites = []string{"store/writer/us_session_statements.go"}
		if defs[n].ID == usSessionDeviceCheck {
			defs[n].Kind = stmt.Read
		}
		if defs[n].ID == usSessionRefreshRetire || defs[n].ID == usSessionRefreshRotate || defs[n].ID == usSessionRefreshReceiptDelete || defs[n].ID == usSessionRefreshReceiptInsert || defs[n].ID == usSessionRefreshPredecessorInsert {
			sample.kind = usSessionRefresh
			sample.request.refreshDigest, sample.request.refreshGeneration, sample.request.refreshExpiresAt = strings.Repeat("e", 64), 1, pins.horizon
			sample.request.priorReceiptDigest, sample.request.output.refreshDigest = strings.Repeat("f", 64), strings.Repeat("0", 64)
		}
		defs[n].Cohorts = []stmt.Cohort{{Name: "point", Bindings: sample.bindings(defs[n].ID, sample.clocks.now)}}
	}
	return defs
}

func (c *usSessionRotation) bindings(id stmt.ID, now string) []any {
	r, p := c.request, c.request.pins
	switch id {
	case usSessionDeviceCheck:
		return []any{p.device, p.account, p.installation}
	case usSessionFamilyAdvance:
		return []any{p.generation + 1, r.output.horizon, p.family, p.server, p.account, p.profile, p.epoch, p.generation, p.authorityGeneration, p.memberIncarnation, p.profileLifetime, p.incarnation[:], p.horizon, now, p.device}
	case usSessionRenewalRetire:
		return []any{p.accessDigest, p.family, p.generation, p.accessExpiresAt, now}
	case usSessionRefreshRetire:
		return []any{p.accessDigest, p.family, p.generation, p.accessExpiresAt}
	case usSessionAccessInsert:
		return []any{r.output.accessDigest, p.family, p.generation + 1, r.output.accessExpiresAt, int64(0)}
	case usSessionRenewalInsert:
		return []any{p.family, r.requestID, p.accessDigest, r.controllerID, r.controllerEpoch, p.generation + 1, r.output.accessExpiresAt, r.output.horizon, r.output.nonce, r.output.ciphertext}
	case usSessionRefreshRotate:
		return []any{r.output.refreshDigest, p.generation + 1, r.output.horizon, p.family, p.device, r.refreshDigest, r.refreshGeneration, r.refreshExpiresAt, now}
	case usSessionRefreshReceiptDelete:
		return []any{r.priorReceiptDigest, p.family}
	case usSessionRefreshReceiptInsert:
		return []any{r.refreshDigest, p.family, r.requestID, r.output.ciphertext, r.output.horizon}
	case usSessionRefreshPredecessorInsert:
		return []any{r.refreshDigest, p.family}
	}
	return nil
}

func (c *usSessionRotation) effectIDs() []stmt.ID {
	ids := []stmt.ID{usSessionDeviceCheck, usSessionFamilyAdvance, usSessionRenewalRetire, usSessionAccessInsert, usSessionRenewalInsert}
	if c.kind == usSessionRefresh {
		ids = []stmt.ID{usSessionDeviceCheck, usSessionFamilyAdvance, usSessionRefreshRetire, usSessionAccessInsert, usSessionRefreshRotate}
		if c.request.priorReceiptDigest != "" {
			ids = append(ids, usSessionRefreshReceiptDelete)
		}
		ids = append(ids, usSessionRefreshReceiptInsert, usSessionRefreshPredecessorInsert)
	}
	return ids
}

// Native prefix ONLY; never Applied. Before calling, W owes same-transaction
// current authority (including independent controller proof or refresh binding,
// locality, PIN/grant/Portico/sign-in policy) and ORIGINAL fresh receipt checks.
// On ANY error it must roll back this entire prefix. Before success it still
// owes typed retained receipts/owner byte accounting/events and proved FULL
// COMMIT. No callback can treat this helper's value as admission or issuance.
func applyUSSessionRotation(ctx context.Context, turn *commandTurn, c *usSessionRotation, trustedNow string) (out usSessionNativeResult, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if c == nil || turn == nil || turn.transaction == nil ||
		turn.transaction.descriptor.ID != c.kind || turn.transaction.descriptor.Durability != Full ||
		c.kind != usSessionRenewal && c.kind != usSessionRefresh {
		return out, ErrInvalid
	}
	now, ok := usSessionTime(trustedNow)
	prepared, valid := usSessionTime(c.clocks.now)
	expiry, expiryOK := usSessionTime(c.request.output.accessExpiresAt)
	if !ok || !valid || !expiryOK || now.Before(prepared) || !expiry.After(now) {
		return out, ErrConflict
	}
	// Check the whole finite recipe before its first mutation. Forecast work and
	// memory are supplied by the parent's root, never fabricated by this helper.
	ids := c.effectIDs()
	for _, id := range ids {
		step, found := turn.steps[id]
		want := stmt.Write
		if id == usSessionDeviceCheck {
			want = stmt.Read
		}
		if !found || step.kind != want {
			return out, invalid("us_session_step")
		}
	}
	for _, id := range ids {
		row, queryErr := turn.queryRow(ctx, turn.steps[id].statementStep, c.bindings(id, trustedNow)...)
		if queryErr != nil {
			return out, queryErr
		}
		var family string
		var deviceRevision int64
		destination := any(&family)
		if id == usSessionDeviceCheck {
			destination = &deviceRevision
		}
		if err = row.Scan(destination); errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		} else if err != nil {
			return out, err
		}
		if id == usSessionDeviceCheck && deviceRevision < 1 || id != usSessionDeviceCheck && family != c.request.pins.family {
			return out, ErrResolveRequired
		}
	}
	out = c.nativeResult()
	return out, turn.checkLifetimes(ctx)
}
