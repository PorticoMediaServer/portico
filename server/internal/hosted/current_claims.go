package hosted

import (
	"context"
	"database/sql"

	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/networking"
)

type currentClaims struct {
	departureKeys *networking.ProtectedKeys
	store         *networking.SQLiteStore
	runner        *networking.AuthorityRunner
	transport     *networking.HTTPTransport
}

func (s *Service) UseCurrentClaims(store *networking.SQLiteStore, runner *networking.AuthorityRunner, transport *networking.HTTPTransport) error {
	if store == nil || runner == nil || transport == nil || !s.Configured() {
		return networking.ErrInvalid
	}
	s.current = &currentClaims{store: store, runner: runner, transport: transport}
	return store.SetInstallationHooks(s.linkClaimOwnerTx, s.NetworkChanged)
}

// RevokeClaimTx is the actual same-transaction terminal/cancel hook. It retains
// immutable keys and cancellation history while fencing every Hosted family.
func (s *Service) RevokeClaimTx(ctx context.Context, tx *sql.Tx, v networking.Intent, state string) error {
	if tx == nil || v.ServerID != s.identity.ID() {
		return networking.ErrStale
	}
	if e := networking.RevokeCertificatesTx(ctx, tx, v.OperationID); e != nil {
		return e
	}
	if e := s.identity.RetireOwnerCompletionForClaimTx(ctx, tx, v.OperationID); e != nil {
		return e
	}
	if _, e := tx.ExecContext(ctx, `DELETE FROM policy WHERE server_id=?`, v.ServerID); e != nil {
		return e
	}
	if e := identity.RevokeFamiliesMatchingTx(ctx, tx, identity.RevokedMembershipRemoved, `authority='hosted'`); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `UPDATE playback_sessions SET state='stopped' WHERE session_hash IN(SELECT hash FROM authorization_access WHERE authority='hosted')`)
	return e
}
