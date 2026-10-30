package hosted

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// IdentityClaims is Hosted's statement "this is Portico Account A, speaking to
// server S, answering challenge C" (POST /v1/servers/{id}/identity on Hosted).
// It says nothing about membership: this server decides that from its own
// accounts table. It is verified offline against the pinned root, so admitting
// a Portico Account member needs no call from this server to Hosted. The field
// order is the wire contract (the payload is re-marshalled and compared).
type IdentityClaims struct {
	Kind        string `json:"kind"`
	AccountID   string `json:"accountId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	ServerID    string `json:"serverId"`
	Challenge   string `json:"challenge"`
	Nonce       string `json:"nonce"`
	IssuedAt    string `json:"issuedAt"`
	ExpiresAt   string `json:"expiresAt"`
}

// An identity assertion signs an account in; a custody assertion is a
// Portico owner accepting custody of this server's claim at Hosted (INT M6).
// Each is accepted only where it is meant.
const (
	identityKind = "portico.identity.v1"
	custodyKind  = "portico.custody.v1"
)

// maximumIdentityLifetime bounds how long an intercepted assertion is worth
// anything; Hosted issues five minutes.
const maximumIdentityLifetime = 10 * time.Minute

// identityClockSkew tolerates a server clock this far off Hosted's (a NAS
// without time sync). Linked members have no password, so a refused assertion
// would lock them out (INT M8).
const identityClockSkew = 5 * time.Minute

// challengeLifetime is how long a sign-in challenge waits for its assertion.
const challengeLifetime = 5 * time.Minute

// ErrClockSkew is an assertion whose times do not fit this server's clock even
// with the tolerance: the server's clock is wrong, or the assertion is stale.
var ErrClockSkew = errors.New("This server's clock does not match Portico's. Check the server's date and time.")

// IssueChallenge gives an installation a single-use challenge to have Hosted
// sign into its identity assertion, so an assertion is fresh, meant for this
// server and presented by the installation that asked (INT M7).
func (s *Service) IssueChallenge(ctx context.Context, installation string) (string, time.Time, error) {
	if s == nil || s.db == nil || len(installation) > 128 {
		return "", time.Time{}, identity.ErrUnauthorized
	}
	raw := make([]byte, 24)
	if _, e := rand.Read(raw); e != nil {
		return "", time.Time{}, e
	}
	challenge := base64.RawURLEncoding.EncodeToString(raw)
	now := time.Now()
	expires := now.Add(challengeLifetime)
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
	if e != nil {
		return "", time.Time{}, e
	}
	defer gated.Rollback()
	tx := gated.Tx()
	if _, e = tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE expires_ms<?`, now.UnixMilli()); e != nil {
		return "", time.Time{}, e
	}
	if _, e = tx.ExecContext(ctx, `INSERT INTO hosted_identity_challenges(challenge_hash,installation_id,expires_ms) VALUES(?,?,?)`, identity.Digest(challenge), installation, expires.UnixMilli()); e != nil {
		return "", time.Time{}, e
	}
	return challenge, expires, gated.Commit()
}

// VerifyIdentity checks a Hosted identity assertion presented by an
// installation, spends its challenge and nonce, and returns the identity with
// the assertion itself as its consent to be listed here. It returns
// identity.ErrUnauthorized for anything that is not a fresh assertion for this
// server answering a challenge this installation was given, including a
// replay, and ErrClockSkew when only the times disagree.
func (s *Service) VerifyIdentity(ctx context.Context, assertion Signed, installation string) (identity.PorticoIdentity, error) {
	return s.verifyAssertion(ctx, assertion, installation, identityKind)
}

// VerifyCustody is VerifyIdentity for a custody assertion: the Portico owner
// accepting custody of this server's claim at Hosted.
func (s *Service) VerifyCustody(ctx context.Context, assertion Signed, installation string) (identity.PorticoIdentity, error) {
	return s.verifyAssertion(ctx, assertion, installation, custodyKind)
}

func (s *Service) verifyAssertion(ctx context.Context, assertion Signed, installation, kind string) (identity.PorticoIdentity, error) {
	var none identity.PorticoIdentity
	if s == nil || !s.Configured() || s.identity == nil {
		return none, identity.ErrUnauthorized
	}
	raw, e := s.verifyEnvelope(ctx, assertion)
	if e != nil || len(raw) > 4096 {
		return none, identity.ErrUnauthorized
	}
	var c IdentityClaims
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&c) != nil {
		return none, identity.ErrUnauthorized
	}
	if canonical, _ := json.Marshal(c); !bytes.Equal(canonical, raw) {
		return none, identity.ErrUnauthorized
	}
	issued, e := time.Parse(time.RFC3339Nano, c.IssuedAt)
	if e != nil {
		return none, identity.ErrUnauthorized
	}
	expires, e := time.Parse(time.RFC3339Nano, c.ExpiresAt)
	if e != nil || c.Kind != kind || c.ServerID != s.identity.ID() || c.AccountID == "" || len(c.AccountID) > 128 ||
		len(c.Nonce) < 22 || len(c.Nonce) > 128 || len(c.Challenge) < 22 || len(c.Challenge) > 128 ||
		!expires.After(issued) || expires.Sub(issued) > maximumIdentityLifetime {
		return none, identity.ErrUnauthorized
	}
	now := time.Now()
	if issued.After(now.Add(identityClockSkew)) || !expires.Add(identityClockSkew).After(now) {
		return none, ErrClockSkew
	}
	consent, e := json.Marshal(assertion)
	if e != nil {
		return none, e
	}
	if s.db != nil {
		// Spend the challenge and the nonce. Expired rows go in the same write;
		// the tables only ever hold the last few minutes of sign-ins.
		gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassSecurityFence)
		if e != nil {
			return none, e
		}
		defer gated.Rollback()
		tx := gated.Tx()
		for _, q := range []string{`DELETE FROM hosted_identity_nonces WHERE expires_ms<?`, `DELETE FROM hosted_identity_challenges WHERE expires_ms<?`} {
			if _, e = tx.ExecContext(ctx, q, now.UnixMilli()); e != nil {
				return none, e
			}
		}
		result, e := tx.ExecContext(ctx, `DELETE FROM hosted_identity_challenges WHERE challenge_hash=? AND installation_id=?`, identity.Digest(c.Challenge), installation)
		if e != nil {
			return none, e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return none, identity.ErrUnauthorized
		}
		keep := expires.Add(identityClockSkew)
		if keep.Before(now) {
			keep = now
		}
		result, e = tx.ExecContext(ctx, `INSERT INTO hosted_identity_nonces(nonce,expires_ms) VALUES(?,?) ON CONFLICT(nonce) DO NOTHING`, c.Nonce, keep.UnixMilli())
		if e != nil {
			return none, e
		}
		if n, e := result.RowsAffected(); e != nil || n != 1 {
			return none, identity.ErrUnauthorized
		}
		if e = gated.Commit(); e != nil {
			return none, e
		}
	}
	return identity.PorticoIdentity{AccountID: c.AccountID, Username: c.Username, DisplayName: c.DisplayName, Email: c.Email, Consent: string(consent), SignerKeyID: assertion.KeyID}, nil
}
