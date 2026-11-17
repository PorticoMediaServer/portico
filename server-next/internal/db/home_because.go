package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

// BecauseSeed is one of the profile's three most recently played films/shows. Both the
// played episode and its show must be visible. Paused/cleared history and reset taste do not seed rows.
type BecauseSeed struct {
	ID              int64
	PublicID, Title string
}

func (d *DB) BecauseSeeds(ctx context.Context, v Viewer, named string) ([]BecauseSeed, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	type play struct {
		BecauseSeed
		at, history int64
	}
	out := []BecauseSeed{}
	seen := map[int64]bool{}
	at, id := int64(1<<62), int64(1<<62)
	for len(out) < 3 {
		rows, err := queryAll(ctx, tx, `SELECT i.id,i.public_id,i.title,h.played_at,h.id FROM play_history h
   CROSS JOIN items leaf ON leaf.id=h.item_id CROSS JOIN items i ON i.id=ifnull(leaf.root_id,leaf.id)
   LEFT JOIN user_item_state s ON s.profile_id=@viewer_profile AND s.item_id=i.id
   WHERE `+historyShown+` AND h.played_at>`+tasteSince+` AND (h.played_at,h.id)<(@at,@history)
    AND leaf.kind IN (`+itoa(int64(KindMovie))+`,`+itoa(int64(KindEpisode))+`) AND i.kind IN (`+itoa(int64(KindMovie))+`,`+itoa(int64(KindShow))+`)
    AND (@named='' OR i.public_id=@named) AND ifnull(s.hidden,0)=0 AND ifnull(s.rating,10)>4
    AND `+acc.visible("leaf")+` AND `+acc.visible("i")+` ORDER BY h.played_at DESC,h.id DESC LIMIT 50`,
			append(v.args(), sql.Named("at", at), sql.Named("history", id), sql.Named("named", named)),
			func(p *play) []any { return []any{&p.ID, &p.PublicID, &p.Title, &p.at, &p.history} })
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			if !seen[p.ID] {
				seen[p.ID] = true
				out = append(out, p.BecauseSeed)
				if named != "" || len(out) == 3 {
					return out, nil
				}
			}
		}
		if len(rows) < 50 {
			break
		}
		last := rows[len(rows)-1]
		at, id = last.at, last.history
	}
	return out, nil
}

func (d *DB) BecauseWatchedPage(ctx context.Context, v Viewer, row string, q ItemQuery) (ItemPage, error) {
	seeds, err := d.BecauseSeeds(ctx, v, strings.TrimPrefix(row, "because:"))
	if err != nil {
		return ItemPage{}, err
	}
	if len(seeds) == 0 {
		return ItemPage{}, ErrNotFound
	}
	q.homeBecause = seeds[0].ID
	if q.Sort == "" {
		q.Sort, q.Desc = SortOrder, true
	}
	return d.Items(ctx, v, q)
}

func (q ItemQuery) becauseSource(w *query, acc access) {
	seed := q.homeBecause
	source := fmt.Sprintf(`(SELECT i.id AS item_id,count(*)+%s AS score FROM (%s) matches
  CROSS JOIN items i ON i.id=matches.item_id WHERE i.id<>%d AND i.kind IN (%d,%d) AND %s
  AND NOT EXISTS(SELECT 1 FROM user_item_state own WHERE own.profile_id=@viewer_profile
   AND own.item_id=i.id AND (own.played=1 OR own.hidden=1)) GROUP BY i.id) because`,
		localTagScore(seed), localMatches(seed), seed, KindMovie, KindShow, acc.visible("i"))
	w.tagTotal, w.plainKinds, w.plainLibrary, w.whole = "", "", 0, false
	w.scope = "SELECT item_id FROM " + source
	w.from += " JOIN " + source + " ON because.item_id=i.id"
	w.countFrom += " JOIN " + source + " ON because.item_id=i.id"
}
