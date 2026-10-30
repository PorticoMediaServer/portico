package social

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// InviteRequest mints a short pairing code. `recipientProfileId` binds the code
// to one profile: the contract prefers explicit invitations, and a bound code
// refuses everybody else with `invite_wrong_recipient` rather than admitting the
// first stranger who guesses it.
type InviteRequest struct {
	ProtocolVersion    string `json:"protocolVersion"`
	ExpiresInMs        int64  `json:"expiresInMs"`
	MaxUses            int    `json:"maxUses"`
	RecipientProfileID string `json:"recipientProfileId"`
}

type Invite struct {
	ID          string `json:"id"`
	GroupID     string `json:"groupId"`
	Code        string `json:"code"`
	ExpiresAt   string `json:"expiresAt"`
	MaxUses     int    `json:"maxUses"`
	Uses        int    `json:"uses"`
	RecipientID string `json:"recipientProfileId"`
}

type InviteResponse struct {
	ProtocolVersion string `json:"protocolVersion"`
	ServerTime      string `json:"serverTime"`
	Invite          Invite `json:"invite"`
}

// CreateInvite issues a code for a group. Only the host may issue one, issuance
// is rate limited per group, and only the code's digest is stored: a database
// read never yields a usable invitation.
func (s *Store) CreateInvite(ctx context.Context, p identity.Principal, id string, req InviteRequest) (InviteResponse, error) {
	var out InviteResponse
	if req.ProtocolVersion != Protocol {
		return out, errInvalid
	}
	if req.ExpiresInMs == 0 {
		req.ExpiresInMs = InviteTTLSeconds * 1000
	}
	if req.MaxUses == 0 {
		req.MaxUses = InviteDefaultUses
	}
	if req.ExpiresInMs < 60000 || req.ExpiresInMs > InviteMaxTTLSeconds*1000 || req.MaxUses < 1 || req.MaxUses > InviteMaxUses {
		return out, errInvalid
	}
	if req.RecipientProfileID != "" && !ValidID(req.RecipientProfileID) {
		return out, errInvalid
	}
	gated, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	g, e := loadGroup(ctx, tx, id)
	if e != nil {
		return out, e
	}
	if g.state == "ended" || g.state == "failed" {
		return out, errGroupEnded
	}
	m, e := loadMember(ctx, tx, id, p.Viewer)
	if e != nil {
		return out, e
	}
	if m.role != "host" || m.state != "joined" {
		return out, errHost
	}
	if e = s.limit(ctx, tx, "invite:"+id, InvitesPerGroupHour, time.Hour); e != nil {
		return out, e
	}
	code, e := newCode(8)
	if e != nil {
		return out, e
	}
	now := s.ms()
	expires := now + req.ExpiresInMs
	inviteID := token("inv_")
	if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_invites(id,group_id,code_digest,created_by,recipient_profile_id,max_uses,expires_at_ms,created_at_ms) VALUES(?,?,?,?,?,?,?,?)`, inviteID, id, digest(code), m.id, req.RecipientProfileID, req.MaxUses, expires, now); e != nil {
		return out, e
	}
	out = InviteResponse{ProtocolVersion: Protocol, ServerTime: stamp(now), Invite: Invite{ID: inviteID, GroupID: id, Code: code, ExpiresAt: stamp(expires), MaxUses: req.MaxUses, RecipientID: req.RecipientProfileID}}
	return out, gated.Commit()
}

// RevokeInvite burns a code before it expires.
func (s *Store) RevokeInvite(ctx context.Context, p identity.Principal, group, invite string) error {
	gated2, e := s.begin(ctx)
	if e != nil {
		return e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	m, e := loadJoinedMember(ctx, tx, group, p.Viewer)
	if e != nil {
		return e
	}
	if m.role != "host" {
		return errHost
	}
	result, e := tx.ExecContext(ctx, `UPDATE social_group_invites SET revoked=1 WHERE id=? AND group_id=?`, invite, group)
	if e != nil {
		return e
	}
	if n, _ := result.RowsAffected(); n == 0 {
		return &Fault{Code: "invite_not_found", Status: 404, Message: "No such invitation."}
	}
	return gated2.Commit()
}

// Join redeems an invite code. Redemption is rate limited per viewer, and the
// contract's state-specific codes (expired, revoked, consumed) are only returned
// once the recipient binding is satisfied: an unbound guesser learns nothing
// about a code beyond `invite_not_found`.
func (s *Store) Join(ctx context.Context, p identity.Principal, req JoinRequest) (GroupResponse, error) {
	var out GroupResponse
	if req.ProtocolVersion != Protocol {
		return out, errInvalid
	}
	code, ok := normalizeCode(req.Code)
	if !ok {
		return out, errInvalid
	}
	if req.DisplayName == "" {
		req.DisplayName = "Viewer"
	}
	if !text(req.DisplayName, 64) {
		return out, errInvalid
	}
	gated3, e := s.begin(ctx)
	if e != nil {
		return out, e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	bucket := rateBucket{"join:" + viewerKey(p.Viewer), JoinAttemptsPerHour}
	if e = s.limit(ctx, tx, bucket.name, bucket.max, time.Hour); e != nil {
		return out, e
	}
	// A refused code is charged in its own transaction: this one rolls back.
	refuse := func(f *Fault) error {
		gated3.Rollback()
		if charge := s.chargeFailure(ctx, []rateBucket{bucket}); charge != nil {
			return charge
		}
		return f
	}
	var inviteID, groupID, recipient string
	var maxUses, uses, revoked int
	var expires int64
	e = tx.QueryRowContext(ctx, `SELECT id,group_id,recipient_profile_id,max_uses,uses,revoked,expires_at_ms FROM social_group_invites WHERE code_digest=?`, digest(code)).Scan(&inviteID, &groupID, &recipient, &maxUses, &uses, &revoked, &expires)
	if errors.Is(e, sql.ErrNoRows) {
		return out, refuse(&Fault{Code: "invite_not_found", Status: 404, Message: "That invitation code is not valid."})
	}
	if e != nil {
		return out, e
	}
	if recipient != "" && recipient != p.ProfileID {
		return out, refuse(&Fault{Code: "invite_wrong_recipient", Status: 403, Message: "That invitation was issued to someone else."})
	}
	now := s.ms()
	switch {
	case revoked != 0:
		return out, refuse(&Fault{Code: "invite_revoked", Status: 410, Message: "That invitation was revoked."})
	case now >= expires:
		return out, refuse(&Fault{Code: "invite_expired", Status: 410, Message: "That invitation has expired."})
	case uses >= maxUses:
		return out, refuse(&Fault{Code: "invite_consumed", Status: 410, Message: "That invitation has already been used."})
	}
	g, e := loadGroup(ctx, tx, groupID)
	if e != nil {
		return out, e
	}
	if g.state == "ended" || g.state == "failed" {
		return out, errGroupEnded
	}
	var member membership
	e = tx.QueryRowContext(ctx, `SELECT id,display_name,role,state,readiness,readiness_position_us,readiness_at_ms,joined_at_ms FROM social_group_members WHERE group_id=? AND authority=? AND account_id=? AND profile_id=?`, groupID, p.Authority, p.AccountID, p.ProfileID).Scan(&member.id, &member.displayName, &member.role, &member.state, &member.readiness, &member.positionUS, &member.readinessMS, &member.joinedMS)
	switch {
	case errors.Is(e, sql.ErrNoRows):
		var joined int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM social_group_members WHERE group_id=? AND state='joined'`, groupID).Scan(&joined); e != nil {
			return out, e
		}
		if joined >= MaxMembers {
			return out, errGroupFull
		}
		member = membership{id: token("mem_"), displayName: req.DisplayName, role: "member", state: "joined", readiness: "buffering", joinedMS: now}
		if _, e = tx.ExecContext(ctx, `INSERT INTO social_group_members(id,group_id,authority,account_id,profile_id,display_name,role,state,readiness,readiness_at_ms,joined_at_ms,seen_at_ms) VALUES(?,?,?,?,?,?,'member','joined','buffering',?,?,?)`, member.id, groupID, p.Authority, p.AccountID, p.ProfileID, req.DisplayName, now, now, now); e != nil {
			return out, e
		}
	case e != nil:
		return out, e
	default:
		// Rejoining after a leave is ordinary: the same profile keeps its member
		// id so queue provenance and receipts stay attached to one participant.
		if _, e = tx.ExecContext(ctx, `UPDATE social_group_members SET state='joined',display_name=?,left_at_ms=NULL,readiness='buffering',readiness_at_ms=?,seen_at_ms=?,readiness_revision=0 WHERE id=?`, req.DisplayName, now, now, member.id); e != nil {
			return out, e
		}
		member.state, member.displayName, member.readiness = "joined", req.DisplayName, "buffering"
	}
	if _, e = tx.ExecContext(ctx, `UPDATE social_group_invites SET uses=uses+1 WHERE id=?`, inviteID); e != nil {
		return out, e
	}
	if e = s.publish(ctx, tx, groupID, EventMembers, map[string]any{"revision": counter(g.revision + 1)}); e != nil {
		return out, e
	}
	if e = s.save(ctx, tx, g, true); e != nil {
		return out, e
	}
	g.revision++
	if out, e = s.project(ctx, tx, p, g, member); e != nil {
		return out, e
	}
	if e = gated3.Commit(); e != nil {
		return out, e
	}
	s.Wake(groupID)
	return out, nil
}
