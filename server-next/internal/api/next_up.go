package api

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/playback"
)

// nextUp is what a session's queue plays after it, as the asking profile may know it: nothing
// for a session with no queue, a queue that ends here, or a title the profile may not see.
func (s *Server) nextUp(ctx context.Context, asking db.Viewer, v playback.Session) (*AccountStreamNext, error) {
	if v.Queue == nil || v.Queue.QueueID == "" {
		return nil, nil
	}
	item, err := s.db.QueueNextItem(ctx, db.Viewer{UserID: v.UserID, ProfileID: v.ProfileID}, v.Queue.QueueID)
	if errors.Is(err, db.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	ids, err := s.db.VisibleItems(ctx, asking, []int64{item})
	if err != nil || ids[item] == "" {
		return nil, err
	}
	titles, err := s.db.VisibleTitles(ctx, asking, []int64{item})
	if err != nil {
		return nil, err
	}
	return &AccountStreamNext{ItemID: ids[item], Title: titles[item]}, nil
}
