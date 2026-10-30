package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
)

func (s *Service) recordStoppedActivityTx(ctx context.Context, tx *sql.Tx, caller Caller, r row, now time.Time) error {
	if r.media == "" || isChannel(r) {
		return nil
	}
	// Cleanup remains possible after revocation, but it must not publish new
	// personal evidence for a refused/replaced media occurrence.
	err := s.Playback.CheckStopOccurrenceTx(ctx, tx, caller.Principal, r.media, r.mediaGeneration, r.item)
	if refusedEvidence(err) || errors.Is(err, playback.ErrOccurrenceChanged) {
		return nil
	}
	if err != nil {
		return err
	}
	return catalog.RecordPlaybackTerminalActivityTx(tx, caller.Principal.Viewer, r.media, r.item, now)
}
