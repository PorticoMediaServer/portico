package identity

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/setupflow"
)

// SetupState reads setup's phase in one snapshot. secret is the caller's setup token,
// or "". While a Portico Account setup is in progress a secret must be that setup's
// own (own is then true); any other secret is ErrUnauthorized. On an open or
// completed server a secret proves nothing and is ignored. delivery may be nil.
func (s *Service) SetupState(ctx context.Context, secret string, now time.Time, delivery setupflow.DeliveryLookup) (state setupflow.State, own bool, err error) {
	read, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return setupflow.State{}, false, err
	}
	defer read.Rollback()
	tx := read.Tx()
	state, err = setupflow.Derive(ctx, tx, now, delivery)
	if err != nil {
		return setupflow.State{}, false, err
	}
	if secret != "" && state.Phase != setupflow.Open && state.Phase != setupflow.Completed {
		if len(secret) != 43 {
			return setupflow.State{}, false, ErrUnauthorized
		}
		var hash string
		err = tx.QueryRowContext(ctx, `SELECT setup_hash FROM onboarding_state_v1 WHERE singleton=1 AND operation_id=?`, state.RequestID).Scan(&hash)
		if errors.Is(err, sql.ErrNoRows) {
			return setupflow.State{}, false, ErrUnauthorized
		}
		if err != nil {
			return setupflow.State{}, false, err
		}
		if subtle.ConstantTimeCompare([]byte(hash), []byte(s.setupHash("initialize-secret", secret))) != 1 {
			return setupflow.State{}, false, ErrUnauthorized
		}
		own = true
	}
	return state, own, read.Commit()
}
