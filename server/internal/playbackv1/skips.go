package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"regexp"

	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
)

var markerIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

// recordSkipTx writes one marker skip in the caller's transaction and returns
// its diagnostics code ("" when nothing new was recorded). An automatic skip
// of a marker the server never authorized for unattended skipping is not
// recorded: diagnostics must not report a policy the server did not make. A
// manual skip is recorded for any currently visible viewer marker of the
// session's item.
func (s *Service) recordSkipTx(ctx context.Context, tx *sql.Tx, r row, k SkipReport) (string, error) {
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM playback_marker_skips WHERE playback_id=? AND marker_id=?`, r.id, k.MarkerID).Scan(&one); err == nil {
		return "", nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	var kind, provenance string
	var confidence float64
	var approved bool
	// Markers, inventory, links and entities are all synchronous facts.
	err := tx.QueryRowContext(ctx, `SELECT m.kind,m.confidence,m.provenance,m.approved FROM analysis_markers m JOIN inventory_objects o ON o.id=m.object_id JOIN catalog_assets asset ON asset.token=o.asset_id JOIN catalog_asset_links link ON link.asset_id=asset.id JOIN catalog_entities item ON item.id=link.entity_id WHERE m.id=? AND item.public_id=pid_blob(?) AND m.deleted=0 LIMIT 1`, k.MarkerID, r.item).Scan(&kind, &confidence, &provenance, &approved)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil // not this item's marker
	}
	if err != nil {
		return "", err
	}
	policy, err := segmentmarkers.ReadConfidencePolicy(ctx, tx)
	if err != nil {
		return "", err
	}
	if !segmentmarkers.ViewerKind(kind) || !policy.ViewerVisible(approved, confidence, provenance) || k.Mode == "automatic" && !policy.AutomaticSafe(approved, confidence, provenance) {
		return "", nil
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO playback_marker_skips(playback_id,marker_id,mode,position_us,generation,created_ms) VALUES(?,?,?,?,?,?)`, r.id, k.MarkerID, k.Mode, k.PositionMs*1000, max(r.generation, 1), s.now().UnixMilli()); err != nil {
		return "", err
	}
	return "marker_skip_" + k.Mode + "_" + kind, nil
}
