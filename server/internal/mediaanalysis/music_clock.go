package mediaanalysis

import (
	"context"
	"database/sql"
	"math"
)

// Basic Musepack uses the validated header's integer count/rate, unlike an
// ffprobe six-decimal duration string. Preserve that sample clock and project
// it to the analysis store's microseconds; playback keeps its exact frames.
func musepackDurationUS(ctx context.Context, tx *sql.Tx, asset, codec string, seconds float64) (int64, error) {
	var observed string
	var rate int
	if err := tx.QueryRowContext(ctx, `SELECT f.codec,f.sample_rate FROM music_technical_facts f JOIN catalog_assets a ON a.token=f.asset_id AND a.size=f.size AND a.modified_ns=f.modified_ns AND a.audio_codec=f.codec WHERE a.token=?`, asset).Scan(&observed, &rate); err != nil {
		return 0, err
	}
	if observed != codec {
		return 0, ErrConflict
	}
	if rate != 32000 && rate != 37800 && rate != 44100 && rate != 48000 {
		return 0, ErrInput
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds <= 0 || seconds > float64((1<<53)-1)/1e6 {
		return 0, ErrInput
	}
	frames := seconds * float64(rate)
	if math.Abs(frames-math.Round(frames)) > .001 {
		return 0, ErrInput
	}
	return int64(math.Round(math.Round(frames) * 1e6 / float64(rate))), nil
}
