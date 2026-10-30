package identity

import (
	"context"
	"database/sql"
)

// The caller has accepted an actual change of owner inside this exact writer.
// Keep integration retirement in that transaction so a failed transfer cannot
// disable the original owner's configuration, or a successful one inherit it.
func (s *Service) ownerChangedTx(ctx context.Context, tx *sql.Tx) error {
	if s.OnOwnerChangedTx != nil {
		return s.OnOwnerChangedTx(ctx, tx)
	}
	return nil
}
