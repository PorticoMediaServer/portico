package networking

import "context"

// AuthorityCapture is public claim identity for the dedicated server-key proof
// authority read. It is not a credential, readiness or cleanup capability.
// The root signer must resolve the exact protected KeyIncarnation while holding
// the same lifecycle lease and recheck this capture before sending its proof.
type AuthorityCapture struct {
	Intent         Intent
	KeyIncarnation string
}

// CaptureClaimAuthority remains available after terminal credential denial and
// removal of bearer ciphertext. It exposes no signing key or credential bytes.
func (s *SQLiteStore) CaptureClaimAuthority(ctx context.Context) (AuthorityCapture, error) {
	gated, e := s.tx(ctx)
	if e != nil {
		return AuthorityCapture{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	id, _, installed, e := readCurrentIdentityTx(ctx, tx)
	if e != nil {
		return AuthorityCapture{}, e
	}
	if !installed.Valid {
		return AuthorityCapture{}, ErrStale
	}
	v, e := loadIntentTx(ctx, tx, installed.String)
	if e != nil {
		return AuthorityCapture{}, e
	}
	if v.Stage != Installed || !validGeneration(v.ClaimGeneration) || !validGeneration(v.CredentialGeneration) {
		return AuthorityCapture{}, ErrStale
	}
	if e = ctx.Err(); e != nil {
		return AuthorityCapture{}, e
	}
	return AuthorityCapture{v.Intent, id.KeyIncarnation}, nil
}
func (s *SQLiteStore) CurrentClaimAuthority(ctx context.Context, expected AuthorityCapture) error {
	actual, e := s.CaptureClaimAuthority(ctx)
	if e != nil {
		return e
	}
	a, b := actual.Intent, expected.Intent
	if actual.KeyIncarnation != expected.KeyIncarnation || !sameBinding(a.Binding, b.Binding) || a.Revision != b.Revision || a.Stage != b.Stage || a.ClaimGeneration != b.ClaimGeneration || a.CredentialGeneration != b.CredentialGeneration {
		return ErrStale
	}
	return ctx.Err()
}
