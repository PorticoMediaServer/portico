package playback

import (
	"context"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// authorizeSourcePreparation closes rejected commands before any descriptor,
// media inspection or file-detail refresh. Release this DB-only snapshot before
// I/O; the existing accepting transaction must still recheck current authority
// and recording retirement after preparation. This is not a remote read lease.
func (s *Service) authorizeSourcePreparation(ctx context.Context, p identity.Principal, item string) (identity.Principal, error) {
	snapshot, err := dbwork.BeginSnapshot(ctx, s.db)
	if err != nil {
		return p, err
	}
	defer snapshot.Rollback()
	if authorize := s.AuthorityTx; authorize != nil {
		p, err = authorize(ctx, snapshot.Tx(), p, item)
		if err != nil {
			return p, err
		}
	}
	return p, recordingAdmittedTx(ctx, snapshot.Tx(), item)
}
