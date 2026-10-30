package playbackv1

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (s *Service) castQueueAuthorityTx(ctx context.Context, tx *sql.Tx, c Caller, queue string) error {
	if c.Principal.Grant != identity.GrantCastTarget {
		return nil
	}
	if s.CastQueueAuthority == nil {
		return identity.ErrForbidden
	}
	return s.CastQueueAuthority(ctx, tx, c, queue)
}
func (s *Service) castQueueStartAuthorityTx(ctx context.Context, tx *sql.Tx, c Caller, queue, entry, item string) error {
	if c.Principal.Grant != identity.GrantCastTarget {
		return nil
	}
	if s.CastQueueStartAuthority == nil || tx == nil {
		return identity.ErrForbidden
	}
	return s.CastQueueStartAuthority(ctx, tx, c, queue, entry, item)
}

func (s *Service) castQueueAuthority(ctx context.Context, c Caller, queue string) error {
	if c.Principal.Grant != identity.GrantCastTarget {
		return nil
	}
	return dbwork.WithReadSnapshot(ctx, s.DB, func(ctx context.Context) error { return s.castQueueAuthorityTx(ctx, dbwork.Snapshot(ctx), c, queue) })
}
