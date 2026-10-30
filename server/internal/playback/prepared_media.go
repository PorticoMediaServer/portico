package playback

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

// OpenPrepared is only a delivery adapter. ResolveGrant/current item access
// remain mandatory at admission and on every response read in HTTP composition.
func (s *Service) OpenPrepared(ctx context.Context, grant string) (*preparedmedia.Reader, bool, error) {
	var session string
	var prepared bool
	e := s.db.QueryRowContext(ctx, `SELECT id,EXISTS(SELECT 1 FROM prepared_media_session_pins pin WHERE pin.session_id=playback_sessions.id) FROM playback_sessions WHERE grant_hash=?`, identity.Digest(grant)).Scan(&session, &prepared)
	if e != nil {
		return nil, false, e
	}
	if !prepared {
		return nil, false, nil
	}
	if s.Prepared == nil {
		return nil, true, ErrIncompatible
	}
	return s.Prepared.OpenSession(ctx, session)
}
