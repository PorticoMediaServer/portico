package db

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// EpisodesWithoutPicture lists a library's episodes, version by version, that have no picture
// at all: none beside the file, none from the provider (fetched or still to fetch) and no frame
// tried from the video. They are the episodes a picture is made for (definition area 4, rule
// 46: "Provider, then made").
func (d *DB) EpisodesWithoutPicture(ctx context.Context, library string, after int64) ([]SegmentEntry, error) {
	return d.segmentEntries(ctx, ` AND i.kind=? AND l.public_id=? AND m.id>? AND m.video_codec IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM images x WHERE x.item_id=i.id AND x.role=?) ORDER BY m.id LIMIT 25`,
		KindEpisode, library, after, ImageThumb)
}

// AddMadePicture records the picture made from an episode's video file, and shows it where
// nothing better is there (selectPicture). An empty hash records that no picture could be
// made from this file, so it is not tried again until the file is replaced. An episode that
// gained a picture since it was listed is left alone.
func (d *DB) AddMadePicture(ctx context.Context, itemID string, fileID int64, hash string) error {
	return d.write(ctx, Background, func(tx *sql.Tx) error {
		b := newBatchTx(tx)
		defer b.close()
		var id, library int64
		err := b.queryRow(`SELECT id, library_id FROM items WHERE public_id = ?`, itemID).Scan(&id, &library)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		res, err := b.exec(`INSERT INTO images (item_id, file_id, role, source, file_hash, width, height)
			SELECT ?1, ?2, ?3, ?4, ?5, ?6, ?7 WHERE NOT EXISTS (SELECT 1 FROM images WHERE item_id = ?1 AND role = ?3)`,
			id, fileID, ImageThumb, ImageFromFrame, nz(hash), 320, 180)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 || hash == "" {
			return nil
		}
		if err := selectPicture(b, id, ImageThumb); err != nil {
			return err
		}
		return appendChange(tx, Change{At: time.Now().UnixMilli(), Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}
