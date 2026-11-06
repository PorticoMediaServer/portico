package writer

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Private CHANGE-008 security components. Fields/NULL groups
// follow adopted CHANGE-008 schema debd5c77.
// This file cannot issue authority, open/register/commit a writer, encode a
// common receipt or append/prune api_events. W's sole composition owns those.
const wc008SecurityDeny stmt.ID = "wc.security.admin_revoke.deny"

func wcSecurityDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{ID: wc008SecurityDeny, Owner: "store/writer WC security", Kind: stmt.Write,
			CallSites: []string{"store/writer/wc_security_008_draft.go"},
			SQL: `UPDATE authorization_session_families
 SET revoked=1,revoked_reason='admin_revoke',revoked_at=?,teardown_due_ms=?
 WHERE id=? AND server_id=? AND account_id=? AND profile_id=? AND authority='local'
 AND epoch=? AND current_generation=? AND command_incarnation=?
 AND command_member_incarnation=? AND command_profile_lifetime=?
 AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND revoked=0 AND revoke_operation_id IS NULL AND control_variable_bytes=?
 RETURNING id,revoked_reason,revoked_at,control_variable_bytes`,
			Params: []stmt.Param{
				{Name: "revoked_at", Type: stmt.Text}, {Name: "due_ms", Type: stmt.Integer},
				{Name: "family", Type: stmt.Text}, {Name: "server", Type: stmt.Text},
				{Name: "account", Type: stmt.Text}, {Name: "profile", Type: stmt.Text},
				{Name: "epoch", Type: stmt.Integer}, {Name: "token_generation", Type: stmt.Integer},
				{Name: "command_incarnation", Type: stmt.Blob}, {Name: "membership_incarnation", Type: stmt.Text},
				{Name: "profile_lifetime", Type: stmt.Text}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "old_control_bytes", Type: stmt.Integer},
			},

			Cohorts: []stmt.Cohort{{Name: "deny", Bindings: []any{"1970-01-01T00:00:01Z", int64(1123), "target", "server", "account", "profile", int64(7), int64(3), []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", int64(0), int64(0), int64(153)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}},
		},
		{ID: wc008SecurityReceipt, Owner: "store/writer WC security", Kind: stmt.Write, CallSites: []string{"store/writer/wc_security_receipts_008_draft.go"}, SQL: `UPDATE authorization_session_families SET command_last_sequence=?,revoke_command_sequence=?,revoke_operation_id=?,revoke_request_digest=?,
 revoke_issuer_account_id=?,revoke_issuer_profile_id=?,revoke_issuer_family_id=?,revoke_issuer_member_incarnation=?,revoke_issuer_profile_lifetime=?,
 revoke_result_reason=?,revoke_result_at=?,revoke_applied_ms=?,revoke_command_kind='admin_revoke',revoke_result_version=?,revoke_result_count=1,revoke_result_digest=?
 WHERE id=? AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=?
 AND revoked=1 AND revoked_reason='admin_revoke' AND revoked_at=? AND revoke_operation_id IS NULL
 AND control_variable_bytes=?
 RETURNING control_variable_bytes`, Params: []stmt.Param{{Name: "last_next", Type: stmt.Integer}, {Name: "result_sequence", Type: stmt.Integer}, {Name: "operation", Type: stmt.Blob}, {Name: "request", Type: stmt.Blob}, {Name: "issuer_account", Type: stmt.Text}, {Name: "issuer_profile", Type: stmt.Text}, {Name: "issuer_family", Type: stmt.Text}, {Name: "issuer_member", Type: stmt.Text}, {Name: "issuer_lifetime", Type: stmt.Text}, {Name: "reason", Type: stmt.Text}, {Name: "at", Type: stmt.Text}, {Name: "applied", Type: stmt.Integer}, {Name: "result_version", Type: stmt.Integer}, {Name: "result", Type: stmt.Blob}, {Name: "family", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "last", Type: stmt.Integer}, {Name: "ack", Type: stmt.Integer}, {Name: "native_at", Type: stmt.Text}, {Name: "deny_control_bytes", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "receipt", Bindings: []any{int64(1), int64(1), []byte{2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, []byte{3, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "account", "profile", "issuer", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "admin_revoke", "1970-01-01T00:00:01Z", int64(1123), int64(1), []byte{4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, "target", []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, int64(0), int64(0), "1970-01-01T00:00:01Z", int64(184)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}}},
	}
}

type wc008SecurityCommand struct {
	family, issuerFamily, server, account, profile string
	memberIncarnation, profileLifetime             string
	identity                                       CommandIdentity
	last, acknowledged, epoch, tokenGeneration     int64
	appliedMS                                      int64
	resultVersion, oldControlBytes                 int64
	revokedAt                                      string // W's trusted native UTC projection
}

// This is an INTERNAL effect result, never Applied or a public receipt. W must
// still save the permanent closed scalar +last sequence, append the ONE devices
// hint through its bounded event-owner helper, and complete FULL/Recovery on
// this SAME turn. Every failure rolls back the whole domain prefix.
type wc008SecurityEffect struct {
	family, reason, revokedAt string
	appliedMS                 int64
	controlVariableBytes      int64
}

func (q wc008SecurityCommand) validate() error {
	if q.family == "" || q.issuerFamily == "" || q.family == q.issuerFamily || q.server == "" ||
		q.account == "" || q.profile == "" || q.memberIncarnation == "" || q.profileLifetime == "" ||
		q.identity.Incarnation == [16]byte{} || q.identity.OperationID == [16]byte{} || q.identity.Sequence <= 0 ||
		q.last < 0 || q.acknowledged != q.last || q.identity.Sequence <= q.last || q.identity.Sequence-q.last != 1 ||
		q.epoch <= 0 || q.tokenGeneration <= 0 || q.appliedMS < 0 || q.resultVersion <= 0 || q.oldControlBytes <= 0 ||
		q.revokedAt != time.UnixMilli(q.appliedMS).UTC().Format(time.RFC3339) {
		return ErrConflict
	}
	return nil
}

// W must already have checked CURRENT independent admin authority (OWNER-all /
// ADMIN-member and its narrow API/grant gate), then classified the ORIGINAL
// identity as fresh. Neither the issuer family string nor this helper grants
// permission. Native target birth/token fields here are a fresh CAS only.
func applyWC008SecurityDeny(ctx context.Context, turn *commandTurn, q wc008SecurityCommand) (out wc008SecurityEffect, err error) {
	if turn == nil {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if err = q.validate(); err != nil {
		return out, err
	}
	step, ok := turn.steps[wc008SecurityDeny]
	if !ok {
		return out, invalid("wc_security_step")
	}
	row, err := turn.queryRow(ctx, step.statementStep, q.revokedAt, q.appliedMS, q.family, q.server, q.account, q.profile,
		q.epoch, q.tokenGeneration, q.identity.Incarnation[:], q.memberIncarnation, q.profileLifetime, q.last, q.acknowledged, q.oldControlBytes)
	if err != nil {
		return out, err
	}
	if err = row.Scan(&out.family, &out.reason, &out.revokedAt, &out.controlVariableBytes); errors.Is(err, sql.ErrNoRows) {
		return wc008SecurityEffect{}, ErrConflict
	} else if err != nil {
		return wc008SecurityEffect{}, err
	}
	if out.family != q.family || out.reason != "admin_revoke" || out.revokedAt != q.revokedAt || out.controlVariableBytes <= 0 {
		return wc008SecurityEffect{}, ErrResolveRequired
	}
	out.appliedMS = q.appliedMS
	return out, turn.checkLifetimes(ctx)
}
