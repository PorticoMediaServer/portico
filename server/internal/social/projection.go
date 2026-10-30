package social

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

func (s *Store) itemFor(ctx context.Context, p identity.Principal, group, item string) (string, error) {
	tx, done, e := dbwork.BeginRead(ctx, s.DB)
	if e != nil {
		return "", e
	}
	defer done()
	if _, e = loadJoinedMember(ctx, tx, group, p.Viewer); e != nil {
		return "", e
	}
	visible, e := s.visible(ctx, tx, p.Viewer, item)
	if !visible {
		return "", e
	}
	return item, e
}
