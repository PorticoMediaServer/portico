package identity

import (
	"context"
	"database/sql"
)

type deviceCaller struct {
	authority, account, role string
	manage                   bool
}

// Every sign-in, including a Portico Account sign-in, belongs to a local account.
func (s *Service) deviceCallerTx(ctx context.Context, tx *sql.Tx, bearer string, manage bool) (deviceCaller, error) {
	c, err := s.directCallerTx(ctx, tx, bearer, false)
	if err != nil {
		return deviceCaller{}, err
	}
	if manage && !c.manage {
		return deviceCaller{}, ErrForbidden
	}
	return deviceCaller{authority: "local", account: c.account.ID, role: c.account.Role, manage: c.manage}, nil
}
func (c deviceCaller) local() bool { return c.authority == "local" }

func (s *Service) accountManagerTx(ctx context.Context, tx *sql.Tx, bearer string) (directCaller, error) {
	c, err := s.directCallerTx(ctx, tx, bearer, false)
	if err != nil {
		return c, err
	}
	if !c.manage {
		return c, ErrForbidden
	}
	return c, nil
}
