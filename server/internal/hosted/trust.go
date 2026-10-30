package hosted

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hostedtrust"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"strings"
	"time"
)

func DefaultConfig(origin, pin, id string) (string, string, string, error) {
	if origin != "" || pin != "" || id != "" {
		if origin == "" || pin == "" || id == "" {
			return "", "", "", errors.New("explicit Hosted override requires origin, root public key and root ID")
		}
		key, e := base64.RawURLEncoding.Strict().DecodeString(pin)
		if e != nil || len(key) != 32 {
			return "", "", "", errors.New("invalid Hosted root override")
		}
		if id != trust.KeyID(key) {
			return "", "", "", errors.New("Hosted root override ID does not match its key")
		}
		u, e := url.Parse(origin)
		if e != nil {
			return "", "", "", e
		}
		if trust.IsDevelopmentRoot(key) && strings.EqualFold(u.Hostname(), "web.getportico.tv") {
			return "", "", "", errors.New("development root cannot use production Hosted origin")
		}
		return origin, pin, id, nil
	}
	root, err := trust.DefaultRoot()
	if err != nil {
		if !trust.Release {
			return "", "", "", nil
		}
		return "", "", "", err
	}
	if trust.IsDevelopmentRoot(root) {
		return "", "", "", nil
	}
	return trust.Origin, base64.RawURLEncoding.EncodeToString(root), trust.KeyID(root), nil
}
func (s *Service) TrustConfig() (string, string, string) {
	return s.origin, base64.RawURLEncoding.EncodeToString(s.key), s.keyID
}
func (s *Service) verifyEnvelope(ctx context.Context, e trust.Envelope) ([]byte, error) {
	return s.verifyEnvelopeAt(ctx, e, time.Now())
}

// verifyEnvelopeAt checks the signing certificate as of at: the moment the
// document was issued, for documents a client stores and presents later (A9:
// an offline profile proof stays valid after the key that signed it is
// renewed). Revocation is always checked as of now.
func (s *Service) verifyEnvelopeAt(ctx context.Context, e trust.Envelope, at time.Time) ([]byte, error) {
	raw, err := e.Verify(s.key, "documents", at)
	if err != nil {
		return nil, err
	}
	if s.db == nil {
		return raw, nil
	} // pure verifier fixtures have no persistence
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err = s.learnRevocations(ctx, e.Certificate); err != nil {
		return nil, err
	}
	var revoked bool
	if err = s.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM hosted_signing_revocations WHERE key_id=?)`, e.KeyID).Scan(&revoked); err != nil {
		return nil, err
	}
	if revoked {
		return nil, trust.ErrTrust
	}
	return raw, nil
}

// learnRevocations records what a root-verified signing certificate says is
// revoked, when it is newer than anything this server has seen, and ends what
// the revoked keys admitted. The caller has verified c against the root.
func (s *Service) learnRevocations(ctx context.Context, c *trust.Certificate) error {
	if c == nil || c.Revision == 0 || c.Revision > trust.MaximumCertificateRevision {
		return trust.ErrTrust
	}
	// Root-signed revocations are permanent and monotonic even across restarts.
	// No transaction or writes for the common case with an unchanged key set.
	var revision uint64
	if err := s.db.QueryRowContext(ctx, `SELECT revision FROM hosted_trust_state WHERE singleton=1`).Scan(&revision); err != nil {
		return err
	}
	if c.Revision <= revision {
		return nil
	}
	gated, err := dbwork.Begin(ctx, s.db, dbwork.ClassInteractive)
	if err != nil {
		return err
	}
	tx := gated.Tx()
	defer gated.Rollback()
	for _, id := range c.Revoked {
		if _, err = tx.ExecContext(ctx, `INSERT OR IGNORE INTO hosted_signing_revocations(key_id) VALUES(?)`, id); err != nil {
			return err
		}
	}
	if _, err = tx.ExecContext(ctx, `UPDATE hosted_trust_state SET revision=max(revision,?) WHERE singleton=1`, c.Revision); err != nil {
		return err
	}
	// A newly learned revocation ends the sessions the revoked key
	// admitted: every sign-in made with one of its assertions, and every
	// session derived from one (a chosen profile, a television it
	// approved, a Cast target it paired). Refusing the key's future
	// assertions is not enough; whoever held the key may already be in.
	if s.identity != nil {
		if _, err = s.identity.RevokeSessionsAdmittedByRevokedKeysTx(ctx, tx); err != nil {
			return err
		}
	}
	// A newly learned root revocation also withdraws cached authorization.
	if err = identity.RevokeFamiliesMatchingTx(ctx, tx, identity.RevokedAdminRevoke, `authority='hosted' AND server_id IN (SELECT server_id FROM hosted_policy_signers p JOIN hosted_signing_revocations r ON r.key_id=p.key_id)`); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM policy WHERE server_id IN (SELECT server_id FROM hosted_policy_signers p JOIN hosted_signing_revocations r ON r.key_id=p.key_id)`); err != nil {
		return err
	}
	return gated.Commit()
}

// learnSigningCertificate takes the current signing certificate Hosted sends
// with a check-in. A server nobody signs in to with a Portico Account sees no
// signed document for weeks, and would otherwise not learn that a key was
// revoked until one arrived; the check-in (which a signed wake brings forward
// when a key changes) tells it directly.
//
// The certificate is trusted for exactly what the root signed: it must verify
// against the pinned root as a documents certificate that is in date. One
// that does not is ignored, and teaches nothing; what this server already
// knows is revoked stays revoked, and an older revision changes nothing.
func (s *Service) learnSigningCertificate(ctx context.Context, c *trust.Certificate) error {
	if c == nil || s.db == nil {
		return nil
	}
	if _, err := c.Verify(s.key, "documents", time.Now()); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return s.learnRevocations(ctx, c)
}
func (s *Service) ValidateTrust(ctx context.Context, e trust.Envelope) error {
	_, err := s.verifyEnvelope(ctx, e)
	return err
}
