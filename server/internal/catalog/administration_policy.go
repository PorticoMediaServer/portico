package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
)

// ApplyScanPolicyTx is the owner administration adapter. Its caller owns the
// writer gate, authorization and aggregate optimistic revision check.
func ApplyScanPolicyTx(ctx context.Context, tx *sql.Tx, library string, p ScanPolicy) error {
	p, err := ValidateScanPolicy(p)
	if err != nil {
		return err
	}
	old, err := ScanPolicyTx(ctx, tx, library)
	if err != nil {
		return err
	}
	a, _ := json.Marshal(p.Operations)
	b, _ := json.Marshal(old.Operations)
	if old.Tier == p.Tier && string(a) == string(b) && old.Trickplay == p.Trickplay {
		return nil
	}
	_, err = tx.ExecContext(ctx, `UPDATE library_scan_policies SET tier=?,operations_json=?,trickplay_interval_seconds=?,trickplay_tile_width=?,trickplay_max_tiles=?,revision=revision+1 WHERE library_id=?`, p.Tier, string(a), p.Trickplay.IntervalSeconds, p.Trickplay.TileWidth, p.Trickplay.MaxTiles, library)
	return err
}
