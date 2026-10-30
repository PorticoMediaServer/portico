package identity

import (
	"context"
	"database/sql"
	"time"
)

// A session grant narrows a session below what its role would allow. A grant
// is fixed when the session is issued and never widens.
const (
	// GrantCastTarget is a screen someone paired as a Cast target. It plays
	// what it is told to play, as the profile that paired it, and can do
	// nothing else: it cannot browse or search, manage the account or the
	// server, approve another device, pair another target, or choose another
	// profile. It is always a member, whoever paired it.
	GrantCastTarget = "cast_target"
	// GrantOwnerClaimCompletion never carries ordinary account or owner rights.
	// Its dedicated operation repository must authorize any completion action.
	GrantOwnerClaimCompletion = "owner_claim_completion"
)

type playbackRequestKey struct{}

// WithPlaybackRequest marks a request as one a Cast target may make: starting,
// controlling and reporting playback, fetching the media and the artwork for
// it, and signing itself out. The HTTP edge decides this from the method and
// path before any handler runs.
func WithPlaybackRequest(ctx context.Context) context.Context {
	return context.WithValue(ctx, playbackRequestKey{}, true)
}

// PermitsRequest refuses a narrowed session outside what it was issued for.
// The refusal is a 403, never a 401: the screen's session is fine, the request
// is not one it may make.
func (p Principal) PermitsRequest(ctx context.Context) error {
	switch p.Grant {
	case "":
		return nil
	case GrantCastTarget:
		if ctx.Value(playbackRequestKey{}) == true {
			return nil
		}
	}
	return ErrForbidden
}

// IssueCastTargetTx issues the session of a screen paired as a Cast target,
// for the profile that paired it. Whatever tier the pairing person holds, the
// screen's session is a member's and carries the Cast target grant, so a
// television in a shared room never holds its owner's authority. The device
// record it registers is vouched for by the pairing session and does not wait
// for the owner's approval. pairingFamily is the session it derives from (the
// one that asked for the code, or the target's previous session on a
// reconnect): the new session inherits its Portico admission, and so ends
// with it if the signing key behind it is revoked.
func (s *Service) IssueCastTargetTx(ctx context.Context, tx *sql.Tx, authority, account, profile string, epoch int, horizon time.Time, pairingFamily string) (Envelope, error) {
	if pairingFamily != "" {
		var rawHorizon string
		if err := tx.QueryRowContext(ctx, `SELECT authorization_horizon FROM authorization_session_families WHERE id=? AND revoked=0`, pairingFamily).Scan(&rawHorizon); err != nil {
			return Envelope{}, ErrUnauthorized
		}
		parentHorizon, err := familyTime(rawHorizon)
		if err != nil || !parentHorizon.After(time.Now()) {
			return Envelope{}, ErrUnauthorized
		}
		ctx = context.WithValue(ctx, inheritedHorizonKey{}, parentHorizon)
	}
	ctx, err := withParentPorticoHorizonTx(ctx, tx, pairingFamily)
	if err != nil {
		return Envelope{}, err
	}
	if pairingFamily != "" {
		// The screen continues the pairing session's PIN-attempt lineage.
		if ctx, err = withPINLineageOf(ctx, tx, pairingFamily); err != nil {
			return Envelope{}, err
		}
	}
	out, err := s.IssueTx(WithVouchedDevice(ctx), tx, account, profile, authority, TierMember, epoch, horizon)
	if err != nil {
		return Envelope{}, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO authorization_family_grants(family_id,grant_kind) VALUES(?,?)`, out.SessionFamilyID, GrantCastTarget); err != nil {
		return Envelope{}, err
	}
	if err = inheritPorticoAdmissionTx(ctx, tx, pairingFamily, out.SessionFamilyID); err != nil {
		return Envelope{}, err
	}
	// The screen is given an access token only. It renews through its own
	// rotating device token (POST /v1/cast/reconnect), so the ordinary refresh
	// credential is never handed out and is removed rather than left unused.
	if _, err = tx.ExecContext(ctx, `DELETE FROM identity_refresh_credentials WHERE family_id=?`, out.SessionFamilyID); err != nil {
		return Envelope{}, err
	}
	out.RefreshToken = ""
	return out, nil
}

// FamilyOfTokenTx is the session family a current access token belongs to, or
// empty when there is none.
func FamilyOfTokenTx(ctx context.Context, tx *sql.Tx, hash string) string {
	return familyForTokenTx(ctx, tx, hash)
}
