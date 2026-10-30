package networking

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/serverreturn"
)

// RegisteredSignInPrepare is the bounded, prepare-only signed display context.
// Its order is shared with the Hosted browser verifier. It grants no session,
// membership or consent and never changes the generic ChallengeProof contract.
type RegisteredSignInPrepare struct {
	Kind               string `json:"kind"`
	Version            string `json:"version"`
	ServerID           string `json:"serverId"`
	PublicKey          string `json:"publicKey"`
	Fingerprint        string `json:"fingerprint"`
	ServerName         string `json:"serverName"`
	RegistrationID     string `json:"registrationId"`
	RegistrationOrigin string `json:"registrationOrigin"`
	HostedOrigin       string `json:"hostedOrigin"`
	ClaimOperationID   string `json:"claimOperationId"`
	Challenge          string `json:"challenge"`
	Nonce              string `json:"nonce"`
	InstallationID     string `json:"installationId"`
	IssuedAt           string `json:"issuedAt"`
	ExpiresAt          string `json:"expiresAt"`
}

type RegisteredSignInPrepareInput struct {
	ServerID, RegistrationID, RegistrationOrigin, HostedOrigin string
	ClaimOperationID, Challenge, Nonce, InstallationID         string
}

// SignRegisteredSignInPrepare reads the real name and current installation in
// one snapshot. Callers cannot supply display text, key facts or an expiry.
func (h *RouteIdentityHandler) SignRegisteredSignInPrepare(parent context.Context, q RegisteredSignInPrepareInput) (out RegisteredSignInPrepare, payload, signature string, err error) {
	if h == nil || h.keys == nil || h.runner == nil || !serverreturn.Origin(q.RegistrationOrigin) || !serverreturn.Origin(q.HostedOrigin) || !validID(q.ClaimOperationID) || len(q.Challenge) < 32 || !validID(q.Challenge) || len(q.InstallationID) < 32 || !validID(q.InstallationID) {
		return out, "", "", ErrInvalid
	}
	nonce, e := base64.RawURLEncoding.Strict().DecodeString(q.Nonce)
	if e != nil || len(nonce) != 32 || base64.RawURLEncoding.EncodeToString(nonce) != q.Nonce {
		return out, "", "", ErrInvalid
	}
	err = h.runner.Do(parent, func(ctx context.Context) error {
		g, e := dbwork.BeginSnapshot(ctx, h.keys.db)
		if e != nil {
			return e
		}
		defer g.Rollback()
		tx := g.Tx()
		id, e := CurrentIdentityTx(ctx, tx)
		if e != nil || id.ServerID != q.ServerID {
			return ErrStale
		}
		stored, e := loadIntentTx(ctx, tx, q.ClaimOperationID)
		if e != nil {
			return e
		}
		if !stored.InstallationAcknowledged {
			return ErrStale
		}
		if e = GuardInstalledClaim(ctx, tx, stored.Intent); e != nil {
			return e
		}
		var kind, name string
		var generation, expiresMS int64
		if e = tx.QueryRowContext(ctx, `SELECT sign_in_kind FROM identity_state WHERE singleton=1`).Scan(&kind); e != nil {
			return e
		}
		if kind != "portico" {
			return ErrUnavailable
		}
		if e = tx.QueryRowContext(ctx, `SELECT generation FROM networking_remote_state WHERE singleton=1`).Scan(&generation); e != nil {
			return e
		}
		claim, ce := strconv.ParseInt(stored.ClaimGeneration, 10, 64)
		credential, ke := strconv.ParseInt(stored.CredentialGeneration, 10, 64)
		if ce != nil || ke != nil || claim < 1 || credential < 1 || generation < 1 || q.RegistrationID != serverreturn.RegistrationID(id.ServerID, stored.OperationID, claim, credential, generation, q.RegistrationOrigin) {
			return ErrStale
		}
		hash := sha256.Sum256([]byte(q.Challenge))
		if e = tx.QueryRowContext(ctx, `SELECT expires_ms FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=?`, base64.RawURLEncoding.EncodeToString(hash[:]), q.InstallationID).Scan(&expiresMS); e != nil {
			return e
		}
		now, expires := time.Now().UTC(), time.UnixMilli(expiresMS).UTC()
		if !expires.After(now) || expires.After(now.Add(5*time.Minute)) {
			return ErrStale
		}
		e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='name'`).Scan(&name)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		// Match canonical Identity.Name(), including its existing default.
		if name == "" {
			name = "Portico"
		}
		if !utf8.ValidString(name) || len(name) > 120 || strings.TrimSpace(name) == "" || strings.IndexFunc(name, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) >= 0 {
			return ErrInvalid
		}
		key, e := h.keys.read(id.KeyIncarnation)
		if e != nil {
			return e
		}
		defer clear(key)
		if !bytes.Equal(key.Public().(ed25519.PublicKey), id.PublicKey) {
			return ErrStale
		}
		fingerprint := sha256.Sum256(id.PublicKey)
		out = RegisteredSignInPrepare{"portico.registered-sign-in-prepare", "1", id.ServerID, base64.RawURLEncoding.EncodeToString(id.PublicKey), base64.RawURLEncoding.EncodeToString(fingerprint[:]), name, q.RegistrationID, q.RegistrationOrigin, q.HostedOrigin, q.ClaimOperationID, q.Challenge, q.Nonce, q.InstallationID, now.Format(time.RFC3339Nano), expires.Format(time.RFC3339Nano)}
		raw, e := json.Marshal(out)
		if e != nil {
			return e
		}
		payload = base64.RawURLEncoding.EncodeToString(raw)
		signature = base64.RawURLEncoding.EncodeToString(ed25519.Sign(key, raw))
		return ctx.Err()
	})
	if err != nil {
		return RegisteredSignInPrepare{}, "", "", err
	}
	return
}
