package db

import (
	"context"
	"database/sql"
	"strings"
)

// EpisodeContext is what Home's hero says about an episode in Continue Watching beyond its
// card (tv-1: "2023 · Drama · 1 season" and a two-line story): the episode's summary, and its
// show's year, first two genres and how many regular seasons the profile may see.
type EpisodeContext struct {
	Summary  string
	ShowYear int
	Genres   []string
	Seasons  int
}

// EpisodeContexts reads the context of the episodes named (public ids), for those the profile
// may see with a show it may see; an episode not in the answer has none.
func (d *DB) EpisodeContexts(ctx context.Context, v Viewer, episodes []string) (map[string]EpisodeContext, error) {
	out := map[string]EpisodeContext{}
	if len(episodes) == 0 {
		return out, nil
	}
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	args := []any{KindSeason}
	for _, id := range episodes {
		args = append(args, id)
	}
	rows, err := tx.QueryContext(ctx, `SELECT e.public_id, ifnull(e.summary, ''), s.id, ifnull(s.year, 0),
		(SELECT count(*) FROM items c WHERE c.parent_id = s.id AND c.kind = ? AND ifnull(c.index_number, 0) > 0 AND `+acc.visible("c")+`)
		FROM items e JOIN items s ON s.id = e.root_id
		WHERE e.public_id IN (?`+strings.Repeat(", ?", len(episodes)-1)+`) AND e.kind = `+itoa(int64(KindEpisode))+`
		AND s.kind = `+itoa(int64(KindShow))+` AND `+acc.visible("e")+` AND `+acc.visible("s"), args...)
	if err != nil {
		return nil, err
	}
	shows := map[int64][]string{}
	for rows.Next() {
		var id string
		var show int64
		var c EpisodeContext
		if err := rows.Scan(&id, &c.Summary, &show, &c.ShowYear, &c.Seasons); err != nil {
			rows.Close()
			return nil, err
		}
		out[id] = c
		shows[show] = append(shows[show], id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(shows) == 0 {
		return out, err
	}
	ids := make([]int64, 0, len(shows))
	for show := range shows {
		ids = append(ids, show)
	}
	genres, err := tx.QueryContext(ctx, `SELECT it.item_id, t.name FROM item_tags it JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN `+intList(ids)+` AND t.kind = 'genre' ORDER BY it.item_id, t.name`)
	if err != nil {
		return nil, err
	}
	defer genres.Close()
	for genres.Next() {
		var show int64
		var name string
		if err := genres.Scan(&show, &name); err != nil {
			return nil, err
		}
		for _, id := range shows[show] {
			if c := out[id]; len(c.Genres) < 2 {
				c.Genres = append(c.Genres, name)
				out[id] = c
			}
		}
	}
	return out, genres.Err()
}
