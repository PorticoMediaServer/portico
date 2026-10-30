package hosted

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

type transactionPolicyReader struct {
	ctx context.Context
	tx  *sql.Tx
}

func (r transactionPolicyReader) QueryRow(query string, args ...any) *sql.Row {
	return r.tx.QueryRowContext(r.ctx, query, args...)
}

// AllowedTxContext reads the same cached membership and local restrictions as
// Allowed, using only the caller's transaction and cancellation context.
func (s *Service) AllowedTxContext(ctx context.Context, principal identity.Principal, library string, tx *sql.Tx) error {
	if tx == nil {
		return identity.ErrUnauthorized
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	err := s.allowed(principal, library, transactionPolicyReader{ctx, tx})
	if cancelled := ctx.Err(); cancelled != nil {
		return cancelled
	}
	return err
}

// AllowedSetTxContext is the local membership policy over a library set. The
// Portico-account path is a server-local membership too; no Hosted call occurs.
func (s *Service) AllowedSetTxContext(ctx context.Context, principal identity.Principal, libraries []string, tx *sql.Tx) (map[string]bool, error) {
	if principal.Authority != "local" || tx == nil {
		return nil, identity.ErrUnauthorized
	}
	all, ids, _, _, err := identity.DirectAccess(transactionPolicyReader{ctx, tx}, principal)
	if err != nil {
		return nil, err
	}
	grants := map[string]bool{}
	for _, id := range ids {
		grants[id] = true
	}
	out := map[string]bool{}
	for _, id := range libraries {
		out[id] = id == "" || all || grants[id]
	}
	return out, ctx.Err()
}
