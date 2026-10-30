package identity

import (
	"context"
	"database/sql"
	"errors"
)

// SignInKind is the server-wide identity authority. Membership and profiles
// always belong to this server, independently of the identity provider.
type SignInKind string

const (
	SignInPortico SignInKind = "portico"
	SignInLocal   SignInKind = "local"
)

func (SignInKind) JSONSchema() map[string]any {
	return map[string]any{"type": "string", "enum": []string{"portico", "local"}}
}

var ErrSignInKindPortico = errors.New("This server uses Portico Accounts. Sign in with your Portico Account.")
var ErrSignInKindLocal = errors.New("This server uses local accounts. Sign in with its username and password.")

type identityReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func SignInKindTx(ctx context.Context, tx *sql.Tx) (SignInKind, error) {
	return readSignInKind(ctx, tx)
}
func readSignInKind(ctx context.Context, db identityReader) (SignInKind, error) {
	var kind SignInKind
	err := db.QueryRowContext(ctx, `SELECT sign_in_kind FROM identity_state WHERE singleton=1`).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return SignInPortico, nil
	}
	if err != nil {
		return "", err
	}
	if kind != SignInPortico && kind != SignInLocal {
		return "", ErrUnauthorized
	}
	return kind, nil
}
func (s *Service) SignInKind(ctx context.Context) (SignInKind, error) {
	return readSignInKind(ctx, s.db)
}
func RequireSignInKindTx(ctx context.Context, tx *sql.Tx, want SignInKind) error {
	got, err := SignInKindTx(ctx, tx)
	if err != nil {
		return err
	}
	return checkSignInKind(got, want)
}
func (s *Service) RequireSignInKind(ctx context.Context, want SignInKind) error {
	got, err := s.SignInKind(ctx)
	if err != nil {
		return err
	}
	return checkSignInKind(got, want)
}
func checkSignInKind(got, want SignInKind) error {
	if got == want {
		return nil
	}
	if got == SignInPortico {
		return ErrSignInKindPortico
	}
	return ErrSignInKindLocal
}

// ManagementCapabilities is read with the session and PIN proof. A selected
// secondary profile never borrows its account's management role.
type ManagementCapabilities struct {
	ManageAccount bool `json:"manageAccount"`
	ManagePeople  bool `json:"managePeople"`
	ManageServer  bool `json:"manageServer"`
	ManageSignIn  bool `json:"manageSignIn"`
}

func (s *Service) RequireMainProfileTx(ctx context.Context, tx *sql.Tx, p Principal) error {
	if _, err := s.SessionFamilyTx(ctx, tx, p); err != nil {
		return err
	}
	var allowed bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM direct_profiles p JOIN accounts a ON a.id=p.account_id WHERE p.account_id=? AND p.id=? AND p.id=a.profile_id AND p.is_primary=1 AND p.deleted=0)`, p.AccountID, p.ProfileID).Scan(&allowed)
	if err != nil {
		return err
	}
	if !allowed || p.Grant != "" || p.Role == "account" {
		return ErrForbidden
	}
	return nil
}

func (s *Service) RevokeMemberSessionsTx(ctx context.Context, tx *sql.Tx, account string) error {
	return s.revokeCredentialsWithReasonTx(ctx, tx, account, RevokedMembershipRemoved)
}
