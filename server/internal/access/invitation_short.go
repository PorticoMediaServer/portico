package access

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

const shortInvitationPurpose = "accept"

type InvitationShortRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	OperationID      string `json:"operationId"`
}
type InvitationShortResponse struct {
	ServerID  string `json:"serverId"`
	ShortCode string `json:"shortCode,omitempty"`
	ExpiresAt string `json:"expiresAt"`
}

func (s *Store) shortInvitationMAC(domain string, tuple ...any) string {
	raw, _ := json.Marshal(tuple)
	hash := hmac.New(sha256.New, s.invitationKey)
	hash.Write([]byte("portico-invitation-short-v1\x00" + domain + "\x00"))
	hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil))
}

// Both identity kinds use the actual current staged server identity; the
// Local-link helper deliberately returns no server ID for Portico invitations.
func invitationServerIDTx(ctx context.Context, tx *sql.Tx) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT i.server_id FROM networking_claim_identity i JOIN networking_server_identities k ON k.server_id=i.server_id AND k.public_key=i.public_key WHERE i.singleton=1`).Scan(&id)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(id, "srv_") {
		return "", identity.ErrUnauthorized
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(id, "srv_"))
	if err != nil || len(raw) != 32 {
		return "", identity.ErrUnauthorized
	}
	return id, nil
}
func invitationKindRevisionTx(ctx context.Context, tx *sql.Tx) (identity.SignInKind, int64, error) {
	kind, err := identity.SignInKindTx(ctx, tx)
	if err != nil {
		return "", 0, err
	}
	var revision int64
	err = tx.QueryRowContext(ctx, `SELECT COALESCE((SELECT revision FROM identity_state WHERE singleton=1),0)`).Scan(&revision)
	return kind, revision, err
}

// IssueShortInvitation explicitly replaces the preceding short capability.
// The receipt never stores plaintext; replay neither reveals nor rotates it.
func (s *Store) IssueShortInvitation(ctx context.Context, auth Authorize, actor identity.Principal, id string, q InvitationShortRequest) (out InvitationShortResponse, err error) {
	if !validID(id) || q.ExpectedRevision < 1 || !validOperationID(q.OperationID) {
		return out, invalid("id", "expectedRevision", "operationId")
	}
	err = s.transaction(ctx, auth, func(tx *sql.Tx) error {
		invitation, e := invitationRow(tx.QueryRowContext(ctx, `SELECT `+invitationColumns+` FROM access_invitations WHERE id=?`, id))
		if e != nil {
			return e
		}
		if e = invitationManageTx(ctx, tx, actor, invitation.Role); e != nil {
			return e
		}
		scope := "invite-short:" + id
		body, want, e := receipt(tx, scope, q.OperationID, q)
		if e != nil {
			return e
		}
		if body != "" {
			return json.Unmarshal([]byte(body), &out)
		}
		if invitation.Revision != q.ExpectedRevision {
			return &ConflictError{invitation.Revision}
		}
		now := s.now()
		expiry, e := time.Parse(time.RFC3339, invitation.ExpiresAt)
		if e != nil {
			return e
		}
		if invitation.State != "pending" || expiry.UnixMilli() <= now {
			return ErrExpired
		}
		server, e := invitationServerIDTx(ctx, tx)
		if e != nil {
			return e
		}
		kind, kindRevision, e := invitationKindRevisionTx(ctx, tx)
		if e != nil {
			return e
		}
		code, e := newShortInvitationCode()
		if e != nil {
			return e
		}
		expires := min(now+int64(10*time.Minute/time.Millisecond), expiry.UnixMilli())
		lookup := s.shortInvitationMAC("lookup", server, kind, shortInvitationPurpose, code)
		binding := s.shortInvitationMAC("binding", server, id, invitation.Revision, kind, kindRevision, shortInvitationPurpose, expires, code)
		_, e = tx.ExecContext(ctx, `INSERT INTO access_invitation_short_codes(invitation_id,lookup_hash,binding_hash,server_id,invitation_revision,sign_in_kind,kind_revision,purpose,expires_ms) VALUES(?,?,?,?,?,?,?,?,?) ON CONFLICT(invitation_id) DO UPDATE SET lookup_hash=excluded.lookup_hash,binding_hash=excluded.binding_hash,server_id=excluded.server_id,invitation_revision=excluded.invitation_revision,sign_in_kind=excluded.sign_in_kind,kind_revision=excluded.kind_revision,purpose=excluded.purpose,expires_ms=excluded.expires_ms`, id, lookup, binding, server, invitation.Revision, kind, kindRevision, shortInvitationPurpose, expires)
		if e != nil {
			return e
		}
		out = InvitationShortResponse{ServerID: server, ExpiresAt: time.UnixMilli(expires).UTC().Format(time.RFC3339Nano)}
		if e = saveReceipt(tx, scope, q.OperationID, want, out, now); e != nil {
			return e
		}
		out.ShortCode, _ = displayShortInvitationCode(code)
		return nil
	})
	return
}

// invitationCapabilityTx returns an internal invitation ID, never a long-link
// secret. Its caller must keep the same snapshot/writer through preview/grant.
func (s *Store) invitationCapabilityTx(ctx context.Context, tx *sql.Tx, long, short string) (string, error) {
	if (long == "") == (short == "") {
		return "", ErrInvitationNotFound
	}
	if short == "" {
		code := strings.TrimSpace(long)
		if len(code) < 32 || len(code) > 128 {
			return "", ErrInvitationNotFound
		}
		var id string
		err := tx.QueryRowContext(ctx, `SELECT id FROM access_invitations WHERE code_hash=? AND state='pending' AND expires_ms>?`, digest(code), s.now()).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrInvitationNotFound
		}
		if err != nil {
			return "", err
		}
		if !s.checkInvitationCode(id, code) {
			return "", ErrInvitationNotFound
		}
		return id, nil
	}
	code, ok := canonicalShortInvitationCode(short)
	if !ok {
		return "", ErrInvitationNotFound
	}
	server, err := invitationServerIDTx(ctx, tx)
	if err != nil {
		return "", err
	}
	kind, kindRevision, err := invitationKindRevisionTx(ctx, tx)
	if err != nil {
		return "", err
	}
	lookup := s.shortInvitationMAC("lookup", server, kind, shortInvitationPurpose, code)
	var id, binding string
	var revision, expires int64
	err = tx.QueryRowContext(ctx, `SELECT c.invitation_id,c.binding_hash,c.invitation_revision,c.expires_ms FROM access_invitation_short_codes c JOIN access_invitations i ON i.id=c.invitation_id
 WHERE c.lookup_hash=? AND c.server_id=? AND c.sign_in_kind=? AND c.kind_revision=? AND c.purpose=? AND c.invitation_revision=i.revision AND i.state='pending' AND c.expires_ms>? AND c.expires_ms<=i.expires_ms AND i.expires_ms>?`, lookup, server, kind, kindRevision, shortInvitationPurpose, s.now(), s.now()).Scan(&id, &binding, &revision, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrInvitationNotFound
	}
	if err != nil {
		return "", err
	}
	expected := s.shortInvitationMAC("binding", server, id, revision, kind, kindRevision, shortInvitationPurpose, expires, code)
	if !hmac.Equal([]byte(binding), []byte(expected)) {
		return "", ErrInvitationNotFound
	}
	return id, nil
}
