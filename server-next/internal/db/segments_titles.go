package db

import "context"

type SegmentTitle struct {
	RowID                int64
	ID, Title, LibraryID string
	Kind                 ItemKind
}

func (d *DB) SegmentTitles(ctx context.Context, library string, after int64, limit int) ([]SegmentTitle, error) {
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	tx, err := d.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	return queryAll(ctx, tx, `SELECT i.id,i.public_id,i.title,l.public_id,i.kind FROM items i JOIN libraries l ON l.id=i.library_id
 WHERE l.public_id=? AND i.id>? AND i.kind IN (?,?) AND i.missing_since_at IS NULL ORDER BY i.id LIMIT ?`,
		[]any{library, after, KindMovie, KindShow, limit}, func(t *SegmentTitle) []any { return []any{&t.RowID, &t.ID, &t.Title, &t.LibraryID, &t.Kind} })
}
func (d *DB) SegmentTitle(ctx context.Context, id string) (SegmentTitle, error) {
	var t SegmentTitle
	err := d.read.QueryRowContext(ctx, `SELECT i.id,i.public_id,i.title,l.public_id,i.kind FROM items i JOIN libraries l ON l.id=i.library_id
 WHERE i.public_id=? AND i.kind IN (?,?)`, id, KindMovie, KindShow).Scan(&t.RowID, &t.ID, &t.Title, &t.LibraryID, &t.Kind)
	return t, notFound(err)
}
func (d *DB) SegmentWaitingShows(ctx context.Context, library, mode string) (int, error) {
	var n int
	err := d.read.QueryRowContext(ctx, `SELECT count(DISTINCT `+segmentTop+`)`+segmentJoins+`
 WHERE l.public_id=? AND i.kind=? AND ?<>'off' AND i.missing_since_at IS NULL AND f.missing_since_at IS NULL
 AND (a.media_id IS NULL OR a.mode<>? OR a.source_key<>`+segmentSourceKey+`)`, library, KindEpisode, mode, mode).Scan(&n)
	return n, err
}

func (d *DB) SegmentVersion(ctx context.Context, item, version string) (SegmentEntry, error) {
	entries, err := d.segmentEntries(ctx, ` AND i.public_id=? AND m.public_id=? LIMIT 1`, item, version)
	if err != nil {
		return SegmentEntry{}, err
	}
	if len(entries) != 1 {
		return SegmentEntry{}, ErrNotFound
	}
	return entries[0], nil
}
