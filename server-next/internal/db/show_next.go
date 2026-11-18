package db

import (
	"context"
	"database/sql"
	"errors"
)

// showNextEpisode reads only this show's state. Recent plays of other shows never push its
// continuation out of a client-sized history window. The next-up step is shared with Home.
func showNextEpisode(ctx context.Context, tx *sql.Tx, v Viewer, acc access, show int64) (*Item, error) {
	args := append(v.args(), sql.Named("show", show), sql.Named("episode", KindEpisode))
	var playing Item
	err := scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns(acc)+itemFrom+`i.root_id=@show AND i.kind=@episode
 AND ifnull(s.played,0)=0 AND (s.position_ms>0 OR s.position_text IS NOT NULL) AND `+acc.visible("i")+`
 ORDER BY s.last_played_at DESC,i.id DESC LIMIT 1`, args...), &playing)
	var items []Item
	switch {
	case err == nil:
		items = []Item{playing}
	case !errors.Is(err, sql.ErrNoRows):
		return nil, err
	default:
		items, err = continueNext(ctx, tx, v, acc, "", nil, nil, 1, show)
		if err != nil {
			return nil, err
		}
	}
	if len(items) == 0 {
		// An unstarted show opens its first unplayed regular episode; a completed one can replay
		// from the beginning, as the existing spotlight does. Specials need a deliberate start.
		err = scanItem(tx.QueryRowContext(ctx, `SELECT `+itemColumns(acc)+itemFrom+`i.root_id=@show AND i.kind=@episode
  AND i.available=1 AND i.parent_index>0 AND `+acc.visible("i")+`
  ORDER BY ifnull(s.played,0),i.parent_index,i.index_number,i.id LIMIT 1`, args...), &playing)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		items = []Item{playing}
	}
	if err := loadImages(ctx, tx, items); err != nil {
		return nil, err
	}
	return &items[0], nil
}
