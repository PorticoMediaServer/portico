package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only the separately CURRENT-authorized admin_revoke variant is supported.
// These values grant no authority and cannot open, issue or dispatch a command.
type SecurityReceiptRequest struct {
	FamilyID string
	Receipt  ReceiptReference
}

const (
	wcSecurityNativeOwner  stmt.ID = "wc.security.protocol_owner"
	wcSecurityNativeResult stmt.ID = "wc.security.protocol_result"
	wcSecurityNativeTarget stmt.ID = "wc.security.protocol_target"
	wcSecurityNativeAck    stmt.ID = "wc.security.protocol_ack"
)

// Captured native target facts, never the issuer's current authority. In
// particular there is no cached family role from which to infer permission.
type wcSecuritySnapshot struct {
	family, server, account, profile   string
	memberIncarnation, profileLifetime string
	incarnation                        [16]byte
	last, acknowledged                 int64
	epoch, tokenGeneration             int64
	revoked, controlBytes              int64
}

type wcSecurityReceiptLookup struct {
	owner    receiptOwnerFacts
	decision receiptDecision
	value    SecurityValue
	digest   ResultDigest
	snapshot wcSecuritySnapshot
	request  SecurityReceiptRequest
	turn     *commandTurn
}

// Protocol-only definitions. The parent composes these with the merged
// wcSecurityDefinitions and wcAPIEventDefinitions, captures the exact registry,
// and supplies its own complete admission/memory/work/FULL/Recovery recipe.
func wcSecurityProtocolDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	inc := []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	defs := []stmt.Definition{
		{ID: wcSecurityNativeOwner, Kind: stmt.Read,
			SQL:     `SELECT account_id,profile_id,command_member_incarnation,command_profile_lifetime,command_incarnation,command_last_sequence,command_acknowledged_sequence,epoch,current_generation,revoked,control_variable_bytes FROM authorization_session_families WHERE id=? AND server_id=? AND authority='local'`,
			Params:  []stmt.Param{text("family"), text("server")},
			Cohorts: []stmt.Cohort{{Name: "original_owner_floor", Bindings: []any{"target", "server"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}}},
		{ID: wcSecurityNativeResult, Kind: stmt.Read,
			SQL:     `SELECT revoke_operation_id,revoke_request_digest,revoke_command_kind,revoke_result_version,revoke_issuer_account_id,revoke_issuer_profile_id,revoke_issuer_member_incarnation,revoke_issuer_profile_lifetime,revoke_issuer_family_id,revoke_result_reason,revoke_result_at,revoke_applied_ms,revoke_result_count,revoke_result_digest FROM authorization_session_families WHERE id=? AND server_id=? AND authority='local' AND command_incarnation=? AND revoke_command_sequence=?`,
			Params:  []stmt.Param{text("family"), text("server"), blob("incarnation"), integer("sequence")},
			Cohorts: []stmt.Cohort{{Name: "permanent_original_scalar", Bindings: []any{"target", "server", inc, int64(1)}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}}},
		{ID: wcSecurityNativeTarget, Kind: stmt.Read,
			SQL:     `SELECT m.role FROM direct_memberships AS m INDEXED BY sqlite_autoindex_direct_memberships_1 INNER JOIN direct_profiles AS p INDEXED BY sqlite_autoindex_direct_profiles_1 ON p.account_id=m.account_id WHERE m.account_id=? AND p.id=? AND m.incarnation=? AND p.lifetime=? AND m.disabled=0 AND p.deleted=0`,
			Params:  []stmt.Param{text("account"), text("profile"), text("member_birth"), text("profile_birth")},
			Cohorts: []stmt.Cohort{{Name: "current_target_births", Bindings: []any{"account", "profile", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_direct_memberships_1", "sqlite_autoindex_direct_profiles_1"}, AutoIndexes: []stmt.AutoIndexBinding{
				{Table: "direct_memberships", Name: "sqlite_autoindex_direct_memberships_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "account_id", Collation: "BINARY"}}},
				{Table: "direct_profiles", Name: "sqlite_autoindex_direct_profiles_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}},
			}}},
		{ID: wcSecurityNativeAck, Kind: stmt.Write,
			SQL:     `UPDATE authorization_session_families INDEXED BY sqlite_autoindex_authorization_session_families_1 SET command_acknowledged_sequence=? WHERE id=? AND server_id=? AND authority='local' AND command_incarnation=? AND command_last_sequence=? AND command_acknowledged_sequence=? AND revoke_command_sequence=? AND revoke_operation_id=? AND revoke_request_digest=? AND revoke_result_digest=? AND revoke_command_kind='admin_revoke' AND revoke_result_count=1 AND revoked=1 RETURNING command_acknowledged_sequence`,
			Params:  []stmt.Param{integer("next_floor"), text("family"), text("server"), blob("incarnation"), integer("last"), integer("old_floor"), integer("original_sequence"), blob("operation"), blob("request"), blob("result")},
			Cohorts: []stmt.Cohort{{Name: "floor_only_keep_scalar", Bindings: []any{int64(1), "target", "server", inc, int64(1), int64(0), int64(1), inc, make([]byte, 32), make([]byte, 32)}}},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_authorization_session_families_1"}, AutoIndexes: []stmt.AutoIndexBinding{{
				Table: "authorization_session_families", Name: "sqlite_autoindex_authorization_session_families_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}},
			}}}},
	}
	for i := range defs {
		defs[i].Owner = "store/writer WC security protocol"
		defs[i].CallSites = []string{"store/writer/wc_security_protocol.go", "store/writer/wc_security_protocol_receipt.go"}
	}
	return defs
}

func validWCSecurityRequest(q SecurityReceiptRequest) bool {
	i := q.Receipt.Identity
	return q.FamilyID != "" && len(q.FamilyID) <= maxCanonicalRequestBytes && utf8.ValidString(q.FamilyID) &&
		i.Incarnation != [16]byte{} && i.OperationID != [16]byte{} && i.Sequence > 0 && q.Receipt.RequestDigest != (RequestDigest{})
}

// CURRENT actor checks precede this private read, including disclosure on
// retries/ACK. A revoked target never creates an exception for a revoked actor.
func lookupWCSecurity(ctx context.Context, turn *commandTurn, q SecurityReceiptRequest, server string) (out wcSecurityReceiptLookup, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SecurityAccept || !validWCSecurityRequest(q) || server == "" || !utf8.ValidString(server) {
		return out, ErrInvalid
	}
	out.request, out.turn = q, turn
	out.snapshot, err = readWCSecurityOwner(ctx, turn, q.FamilyID, server)
	if err != nil && !errors.Is(err, ErrOwnerUnavailable) {
		return out, err
	}
	if err == nil {
		s := out.snapshot
		out.owner = receiptOwnerFacts{true, s.incarnation, s.last, s.acknowledged}
	}
	expected := receiptIdentity{q.Receipt.Identity, q.Receipt.RequestDigest}
	o := out.owner
	// Read neither the permanent scalar nor current target permission before
	// the native floor. An ACK retry remains retired even after source changes.
	if !o.exists || o.incarnation != expected.command.Incarnation || expected.command.Sequence <= o.acknowledgedSequence ||
		o.lastSequence < 0 || o.acknowledgedSequence < 0 || o.acknowledgedSequence > o.lastSequence || o.lastSequence-o.acknowledgedSequence > 1 {
		out.decision, err = classifyReceipt(expected, SecurityAccept, o, receiptFacts{})
		return out, err
	}
	return readWCSecurityOriginal(ctx, turn, out)
}

// Native capture point for preparation and the parent's target disclosure
// check. It does not read a scalar receipt or infer issuer/target permission.
func readWCSecurityOwner(ctx context.Context, turn *commandTurn, family, server string) (out wcSecuritySnapshot, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SecurityAccept || family == "" || server == "" || !utf8.ValidString(family) || !utf8.ValidString(server) {
		return out, ErrInvalid
	}
	out.family, out.server = family, server
	row, err := wc008DraftQuery(ctx, turn, wcSecurityNativeOwner, family, server)
	if err != nil {
		return out, err
	}
	var incarnation []byte
	err = row.Scan(&out.account, &out.profile, &out.memberIncarnation, &out.profileLifetime, &incarnation, &out.last, &out.acknowledged, &out.epoch, &out.tokenGeneration, &out.revoked, &out.controlBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return out, ErrOwnerUnavailable
	}
	if err != nil {
		return out, err
	}
	if len(incarnation) != 16 {
		return out, ErrResolveRequired
	}
	copy(out.incarnation[:], incarnation)
	return out, nil
}

// This exact native point is for the parent's CURRENT target disclosure gate
// and the fresh adapter. It never reads f.role. Birth replacement, deletion and
// disabled membership fail closed rather than lending the old family a tier.
func readWCSecurityTargetTier(ctx context.Context, turn *commandTurn, s wcSecuritySnapshot) (tier string, err error) {
	defer func() { tier, err = finishCommand(ctx, commandLifetime(turn), tier, err) }()
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != SecurityAccept || s.account == "" || s.profile == "" || len(s.memberIncarnation) != 32 || len(s.profileLifetime) != 32 {
		return "", ErrInvalid
	}
	row, err := wc008DraftQuery(ctx, turn, wcSecurityNativeTarget, s.account, s.profile, s.memberIncarnation, s.profileLifetime)
	if err != nil {
		return "", err
	}
	if err = row.Scan(&tier); err != nil {
		return "", errors.Join(ErrDisclosureDenied, err)
	}
	if tier != "owner" && tier != "admin" && tier != "member" {
		return "", ErrDisclosureDenied
	}
	return tier, nil
}

// The caller has independently established the actor's CURRENT effective tier.
// A different family in the same account is allowed for OWNER; account equality
// is not the self-signout distinction. ADMIN can target only a real MEMBER.
func wcSecurityTargetTierAllowed(currentActorTier, currentTargetTier string) bool {
	return currentActorTier == "owner" && (currentTargetTier == "owner" || currentTargetTier == "admin" || currentTargetTier == "member") ||
		currentActorTier == "admin" && currentTargetTier == "member"
}
