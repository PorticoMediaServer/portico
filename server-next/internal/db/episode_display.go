package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

// EpisodeDisplay is how this show's episodes are listed on every device.
type EpisodeDisplay struct {
	Grouping  string `json:"grouping"`  // season, list or ranges (groups of 100)
	Numbering string `json:"numbering"` // season or absolute
	Order     string `json:"order"`     // oldest or newest
}

func (d *DB) SetEpisodeDisplay(ctx context.Context, v Viewer, publicID string, display EpisodeDisplay) error {
	if display.Grouping != "season" && display.Grouping != "list" && display.Grouping != "ranges" ||
		display.Numbering != "season" && display.Numbering != "absolute" || display.Order != "oldest" && display.Order != "newest" {
		return invalid("Choose the show's episode grouping, numbering and order.")
	}
	raw, _ := json.Marshal(display)
	return d.write(ctx, User, func(tx *sql.Tx) error {
		acc, err := loadAccess(ctx, tx, v)
		if err != nil {
			return err
		}
		var id, library int64
		err = tx.QueryRowContext(ctx, `SELECT i.id, i.library_id FROM items i WHERE i.public_id = @id AND i.kind = `+itoa(int64(KindShow))+` AND `+acc.visible("i"),
			append(v.args(), sql.Named("id", publicID))...).Scan(&id, &library)
		if err != nil {
			return notFound(err)
		}
		now := time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `UPDATE items SET extra = json_set(ifnull(extra, '{}'), '$.episodeDisplay', json(?)),
			updated_at = ?, revision = revision + 1 WHERE id = ?`, string(raw), now, id); err != nil {
			return err
		}
		return appendChange(tx, Change{At: now, Topic: TopicItem, Op: OpUpdated, RefID: id, LibraryID: library})
	})
}
