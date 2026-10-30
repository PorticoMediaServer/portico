package playback

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"time"
)

func (h *HLS) rememberConversionFailure(ctx context.Context, id, code, diagnostic string) {
	write, cancel := persist(ctx)
	defer cancel()
	diagnostic = sanitizeConverterOutput(diagnostic, h.root, h.memoryRoot)
	if len(diagnostic) > 2000 {
		diagnostic = string([]rune(diagnostic)[:min(2000, len([]rune(diagnostic)))])
	}
	gated, err := dbwork.Begin(write, h.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return
	}
	defer gated.Rollback()
	tx := gated.Tx()
	_, err = tx.ExecContext(write, `INSERT INTO playback_conversion_failures(session_id,recorded_ms,failure_code,diagnostic,strategy,backend,video,audio) SELECT ?,?,?,?,COALESCE(d.strategy,''),COALESCE(d.hardware_backend,''),COALESCE(d.source_video,''),COALESCE(d.source_audio,'') FROM playback_sessions ps LEFT JOIN playback_delivery_plans d ON d.session_id=ps.id WHERE ps.id=?`, id, time.Now().UnixMilli(), code, diagnostic, id)
	if err != nil {
		return
	}
	_, err = tx.ExecContext(write, `DELETE FROM playback_conversion_failures WHERE sequence NOT IN (SELECT sequence FROM playback_conversion_failures ORDER BY sequence DESC LIMIT 200) OR recorded_ms<?`, time.Now().Add(-30*24*time.Hour).UnixMilli())
	if err == nil {
		_ = gated.Commit()
	}
}
