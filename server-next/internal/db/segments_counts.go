package db

import "context"

type SegmentCounts struct{ Pending, Complete, Failed, Unsupported int }
type SegmentMarkerCounts struct{ Automatic, Suggested, Approved, Dismissed int }

const segmentSourceKey = `cast(f.id AS TEXT)||':'||f.size||':'||f.modified_ns||':'||ifnull(mp.start_ms,0)||':'||ifnull(mp.end_ms,0)
 ||':'||(SELECT count(*) FROM media_parts WHERE media_id=m.id)||':'||ifnull(m.duration_ms,0)||':'||ifnull(m.hdr,'')`

func (d *DB) SegmentCounts(ctx context.Context, library, title, mode string) (SegmentCounts, SegmentMarkerCounts, error) {
	var c SegmentCounts
	var markers SegmentMarkerCounts
	err := d.read.QueryRowContext(ctx, `SELECT
 count(*) FILTER(WHERE ?<>'off' AND (a.media_id IS NULL OR a.mode<>? OR a.source_key<>`+segmentSourceKey+`)),
 count(*) FILTER(WHERE a.mode=? AND a.source_key=`+segmentSourceKey+` AND a.state='complete'),
 count(*) FILTER(WHERE a.mode=? AND a.source_key=`+segmentSourceKey+` AND a.state='failed'),
 count(*) FILTER(WHERE a.mode=? AND a.source_key=`+segmentSourceKey+` AND a.state='unsupported')`+segmentJoins+`
 WHERE i.kind IN (?,?) AND i.missing_since_at IS NULL AND f.missing_since_at IS NULL AND l.public_id=?
 AND (?='' OR `+segmentTop+`=?)`, mode, mode, mode, mode, mode, KindMovie, KindEpisode, library, title, title).
		Scan(&c.Pending, &c.Complete, &c.Failed, &c.Unsupported)
	if err != nil {
		return c, markers, err
	}
	err = d.read.QueryRowContext(ctx, `SELECT count(*) FILTER(WHERE k.source=? AND k.dismissed=0),
 count(*) FILTER(WHERE k.source=? AND k.dismissed=0),count(*) FILTER(WHERE k.source=? AND k.dismissed=0),
 count(*) FILTER(WHERE k.dismissed=1) FROM media_markers k JOIN media m ON m.id=k.media_id JOIN items i ON i.id=m.item_id
 JOIN libraries l ON l.id=i.library_id LEFT JOIN items p ON p.id=i.parent_id LEFT JOIN items g ON g.id=p.parent_id
 WHERE l.public_id=? AND k.kind<>? AND (?='' OR `+segmentTop+`=?)`,
		MarkerConfirmed, MarkerFromAnalysis, MarkerFromPerson, library, MarkerChapter, title, title).
		Scan(&markers.Automatic, &markers.Suggested, &markers.Approved, &markers.Dismissed)
	return c, markers, err
}

func (d *DB) SegmentFailures(ctx context.Context, library string) ([]SegmentEntry, error) {
	return d.segmentEntries(ctx, ` AND l.public_id=? AND a.state IN ('failed','unsupported') ORDER BY a.updated_at DESC LIMIT 32`, library)
}

func (d *DB) SegmentChapters(ctx context.Context, media int64) ([]Marker, error) {
	tx, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT kind,source,start_ms,end_ms,ifnull(title,'') FROM media_markers WHERE media_id=? AND kind=? ORDER BY start_ms`,
		[]any{media, MarkerChapter}, func(m *Marker) []any { return []any{&m.Kind, &m.Source, &m.StartMS, &m.EndMS, &m.Title} })
}

func (d *DB) SegmentPendingEntries(ctx context.Context, library, mode string, after int64) ([]SegmentEntry, error) {
	return d.segmentEntries(ctx, ` AND l.public_id=? AND m.id>? AND (a.media_id IS NULL OR a.mode<>? OR a.source_key<>`+segmentSourceKey+`)
 ORDER BY m.id LIMIT 25`, library, after, mode)
}
