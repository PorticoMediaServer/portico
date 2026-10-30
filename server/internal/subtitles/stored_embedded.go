package subtitles

import (
	"context"
	"database/sql"
	"errors"
)

// StoredEmbedded returns the embedded tracks and timing the last probe of this
// exact file recorded, so a sidecar-only change can rediscover sidecars around
// them without probing the file again. ok is false when no inventory was
// recorded for this size and modification time.
func StoredEmbedded(ctx context.Context, db *sql.DB, asset string, size, modified int64) (tracks []Track, originUS int64, known, ok bool, err error) {
	var status string
	err = db.QueryRowContext(ctx, `SELECT origin_us,timing_known,status FROM asset_subtitle_facts WHERE asset_id=? AND size=? AND modified_ns=?`, asset, size, modified).Scan(&originUS, &known, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, false, false, nil
	}
	if err != nil || status != "known" {
		return nil, 0, false, false, err
	}
	rows, err := db.QueryContext(ctx, `SELECT format,language,title,is_default,is_forced,reason,stream_index,hearing_impaired FROM asset_subtitles WHERE asset_id=? AND origin='embedded' ORDER BY stream_index`, asset)
	if err != nil {
		return nil, 0, false, false, err
	}
	defer rows.Close()
	tracks = []Track{}
	for rows.Next() {
		t := Track{Origin: "embedded"}
		if err = rows.Scan(&t.Format, &t.Language, &t.Title, &t.Default, &t.Forced, &t.Reason, &t.StreamIndex, &t.HearingImpaired); err != nil {
			return nil, 0, false, false, err
		}
		tracks = append(tracks, t)
	}
	if err = rows.Err(); err != nil {
		return nil, 0, false, false, err
	}
	return tracks, originUS, known, true, nil
}
