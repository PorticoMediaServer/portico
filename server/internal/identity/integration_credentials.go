package identity

import (
	"context"
	"database/sql"
)

// IntegrationPrincipalTx binds an owner-created app credential to one actual
// account/profile. It creates no account, trust grant or sign-in session and
// applies the same installed membership predicate as a normal credential.
func (s *Service) IntegrationPrincipalTx(ctx context.Context, tx *sql.Tx, account, profile string) (Principal, error) {
	p := Principal{Viewer: Viewer{AccountID: account, ProfileID: profile, ServerID: s.serverID, Authority: "local"}}
	var primary bool
	err := tx.QueryRowContext(ctx, `SELECT a.epoch,m.role,p.is_primary FROM accounts a JOIN direct_memberships m ON m.account_id=a.id JOIN direct_profiles p ON p.account_id=a.id WHERE a.id=? AND p.id=?`, account, profile).Scan(&p.Epoch, &p.Role, &primary)
	if err == sql.ErrNoRows {
		return Principal{}, ErrUnauthorized
	}
	if err != nil {
		return Principal{}, err
	}
	if !primary {
		p.Role = TierMember
	}
	if err = s.checkFamilyPrincipalTx(ctx, tx, p); err != nil {
		return Principal{}, err
	}
	return p, nil
}

// RequireCredentialManagementTx preserves the native rule that a file-reset
// session cannot create a durable descendant or recover one to escape its
// bounded reset flow. It is used in addition to the owner's live tier proof.
func (s *Service) RequireCredentialManagementTx(ctx context.Context, tx *sql.Tx, p Principal) error {
	family, err := s.SessionFamilyTx(ctx, tx, p)
	if err != nil {
		return err
	}
	return refuseResetDescendantTx(ctx, tx, family.ID)
}
