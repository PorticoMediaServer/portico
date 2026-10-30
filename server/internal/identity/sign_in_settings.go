package identity

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

type SignInOwner struct {
	AccountID   string `json:"accountId"`
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email,omitempty"`
}
type SignInSettings struct {
	SignIn  SignInKind   `json:"signIn"`
	Owner   SignInOwner  `json:"owner"`
	Move    *AccountMove `json:"move"`
	WebPath string       `json:"webPath"`
}

func (s *Service) ReadSignInSettings(ctx context.Context, bearer string) (out SignInSettings, err error) {
	g, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return out, e
	}
	defer g.Rollback()
	tx := g.Tx()
	if _, e = s.moveOwnerTx(ctx, tx, bearer); e != nil {
		return out, e
	}
	if out.SignIn, e = SignInKindTx(ctx, tx); e != nil {
		return out, e
	}
	if e = tx.QueryRowContext(ctx, `SELECT a.id,a.username,a.display_name,a.email FROM accounts a JOIN direct_memberships m ON m.account_id=a.id WHERE m.role='owner' AND m.disabled=0`).Scan(&out.Owner.AccountID, &out.Owner.Username, &out.Owner.DisplayName, &out.Owner.Email); e != nil {
		return out, e
	}
	out.WebPath = "/settings?scope=server&section=signin"
	var id string
	e = tx.QueryRowContext(ctx, `SELECT id FROM identity_moves WHERE stage<>'cancelled' ORDER BY created_ms DESC,id DESC LIMIT 1`).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	move, e := readMoveTx(ctx, tx, id)
	out.Move = &move
	return out, e
}
