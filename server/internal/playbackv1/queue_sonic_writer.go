package playbackv1

import (
	"context"
	"database/sql"
)

// Writers only: projections use withMusicLayout so they never upgrade a read
// connection. The callback receives the current layout even after a span edit
// copies or replaces it; it holds no stale layout pointer.
func (s *Service) readMusicWriterLayout(ctx context.Context, tx *sql.Tx, q queueRow, caller Caller) (*layout, error) {
	l, err := s.readLayout(ctx, tx, q)
	if err != nil {
		return nil, err
	}
	l.sonicPrepare = func(current *layout, window int64) error {
		return s.captureMusicWindowTx(ctx, tx, caller, current, window)
	}
	return l, nil
}

// Cache the current projection before its transaction ends. In particular, a
// no-op command cannot leave a lazy writer callback pointing at a closed Tx.
func (s *Service) finishMusicWriterLayout(l *layout) error {
	s.header(l)
	l.sonicPrepare = nil
	return l.sonicErr
}
