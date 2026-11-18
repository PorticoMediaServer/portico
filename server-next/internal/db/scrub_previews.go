package db

import (
	"context"
	"database/sql"
	"errors"
)

// ScrubPreview is the fixed geometry and stored sheets of one version's navigation pictures.
type ScrubPreview struct {
	Version                                            string
	DurationMS, IntervalUS                             int64
	FrameWidth, FrameHeight, Columns, Rows, FrameCount int
	Sheets                                             []string
}

// ScrubPreviewEntries reads only video sources not yet tried in their current form.
func (d *DB) ScrubPreviewEntries(ctx context.Context, library string, after int64) ([]SegmentEntry, error) {
	return d.segmentWalk(ctx, ` AND l.public_id=? AND m.id>? AND m.video_codec IS NOT NULL
 AND (lp.remote_source_id IS NULL OR EXISTS (SELECT 1 FROM remote_sources rs WHERE rs.id=lp.remote_source_id AND json_extract(rs.config,'$.preset')='everything'))
 AND NOT EXISTS (SELECT 1 FROM scrub_previews sp WHERE sp.media_id=m.id AND sp.source_key=`+segmentSourceKey+`)
 ORDER BY m.id LIMIT 25`, library, after)
}

// SaveScrubPreview records a completed set only while the source still matches. A removed
// title or changed file leaves its unreferenced sheets for the ordinary picture sweep.
func (d *DB) SaveScrubPreview(ctx context.Context, e SegmentEntry, p ScrubPreview) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		current, err := segmentCurrentKey(tx, e)
		if errors.Is(err, sql.ErrNoRows) || err == nil && current != e.Key() {
			return nil
		}
		if err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM scrub_previews WHERE media_id=?`, e.MediaID); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO scrub_previews(media_id,source_key,duration_ms,interval_us,frame_width,frame_height,columns,rows,frame_count) VALUES(?,?,?,?,?,?,?,?,?)`,
			e.MediaID, e.Key(), e.DurationMS, p.IntervalUS, p.FrameWidth, p.FrameHeight, p.Columns, p.Rows, p.FrameCount); err != nil {
			return err
		}
		for n, hash := range p.Sheets {
			if _, err = tx.Exec(`INSERT INTO scrub_preview_sheets(media_id,position,file_hash) VALUES(?,?,?)`, e.MediaID, n, hash); err != nil {
				return err
			}
		}
		return nil
	})
}

// ScrubPreviewFor checks visibility on every descriptor and sheet fetch, including after a
// profile's restrictions change. An absent measurement answers an unavailable set; a title or
// version the viewer may not see is not found. It never starts work on a request's path.
func (d *DB) ScrubPreviewFor(ctx context.Context, v Viewer, item, version string) (ScrubPreview, error) {
	out := ScrubPreview{Sheets: []string{}}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return out, err
	}
	var media int64
	err = tx.QueryRowContext(ctx, `SELECT m.id,m.public_id FROM items i JOIN media m ON m.item_id=i.id
 WHERE i.public_id=? AND (?='' OR m.public_id=?) AND i.missing_since_at IS NULL AND `+acc.visible("i")+` ORDER BY m.position,m.id LIMIT 1`, item, version, version).Scan(&media, &out.Version)
	if err != nil {
		return out, notFound(err)
	}
	err = tx.QueryRowContext(ctx, `SELECT sp.duration_ms,sp.interval_us,sp.frame_width,sp.frame_height,sp.columns,sp.rows,sp.frame_count
 FROM scrub_previews sp JOIN media m ON m.id=sp.media_id JOIN media_parts mp ON mp.media_id=m.id AND mp.position=0 JOIN files f ON f.id=mp.file_id
 WHERE sp.media_id=? AND f.missing_since_at IS NULL AND sp.source_key=`+segmentSourceKey, media).
		Scan(&out.DurationMS, &out.IntervalUS, &out.FrameWidth, &out.FrameHeight, &out.Columns, &out.Rows, &out.FrameCount)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	out.Sheets, err = queryAll(ctx, tx, `SELECT file_hash FROM scrub_preview_sheets WHERE media_id=? ORDER BY position`, []any{media}, func(hash *string) []any { return []any{hash} })
	return out, err
}
