package httpapi

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/livechannels"
)

// liveTunerAvailable says whether a live source has a tuner for one more viewer
// (spec §11: tuner exhaustion is 409 no_tuner_available at the start). Its claims
// are the tuners held (recordings) and the live v1 channel sessions
// on the source that hold none yet, less the session being replaced (surfing
// frees its own tuner). An unknown capacity never refuses.
func liveTunerAvailable(ctx context.Context, tx *sql.Tx, sourceID, channelID, replacing string) (bool, error) {
	capacity, err := livechannels.CapacityTx(ctx, tx, sourceID)
	if errors.Is(err, sql.ErrNoRows) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if !capacity.Known {
		return true, nil
	}
	var claims, shared int
	now := time.Now().UnixMilli()
	err = tx.QueryRowContext(ctx, `SELECT count(DISTINCT channel),COALESCE(max(CASE WHEN channel=? AND reusable=1 THEN 1 ELSE 0 END),0) FROM (
 SELECT CASE WHEN a.channel_id='' THEN a.id ELSE a.channel_id END AS channel,CASE WHEN a.state='active' AND a.lease_until_ms>? THEN 1 ELSE 0 END AS reusable FROM live_allocations a WHERE a.source_id=? AND a.state!='released' AND a.resource_id<>?
 UNION ALL SELECT c.channel_id,1 FROM playback_channel_sessions c JOIN playback_v1_sessions v ON v.id=c.session_id
 WHERE c.kind='live-source' AND c.source_id=? AND c.ended_ms=0 AND v.ended_ms=0 AND v.lease_expires_ms>? AND c.session_id<>?
 )`, channelID, now, sourceID, replacing, sourceID, now, replacing).Scan(&claims, &shared)
	if err != nil {
		return false, err
	}
	return shared != 0 || claims < capacity.Effective, nil
}
