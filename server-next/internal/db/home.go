package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Home rows the server composes.
const (
	continueWindow = 16 * 7 * 24 * time.Hour // a title untouched for longer drops off Continue Watching
	homeRowSize    = 40
)

// ContinueWatching lists what the viewer has started and not finished, most recent first. A
// title the viewer removed from the row stays off it until it is played again. Not interested
// does not take a title off it: that is for recommendation rows. Books are in Continue your
// books instead. It covers every library: Home's row.
func (d *DB) ContinueWatching(ctx context.Context, v Viewer) ([]Item, error) {
	return d.continueRow(ctx, v, "NOT IN", "")
}

// ContinueWatchingIn is ContinueWatching for one library, by its public id: that library's
// landing page. "" is every library.
func (d *DB) ContinueWatchingIn(ctx context.Context, v Viewer, library string) ([]Item, error) {
	return d.continueRow(ctx, v, "NOT IN", library)
}

// ContinueReading lists the books, comics and issues the viewer has a place in and has not
// finished, most recently read first: Home's Continue your books.
func (d *DB) ContinueReading(ctx context.Context, v Viewer) ([]Item, error) {
	return d.continueRow(ctx, v, "IN", "")
}

// ContinueReadingIn is ContinueReading for one library, by its public id: a reading library's
// Continue reading. "" is every library.
func (d *DB) ContinueReadingIn(ctx context.Context, v Viewer, library string) ([]Item, error) {
	return d.continueRow(ctx, v, "IN", library)
}

func (d *DB) continueRow(ctx context.Context, v Viewer, reading, library string) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	// Read from the viewer's own recent state, newest first, and stop at a row's worth.
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns(acc)+` FROM user_item_state s CROSS JOIN items i ON i.id = s.item_id
		WHERE s.profile_id = @viewer_profile AND (s.last_played_at > @since OR i.kind = `+fmt.Sprintf("%d", KindEpisode)+`) AND s.played = 0 AND s.last_played_at > ifnull(s.continue_dismissed_at, 0)
		AND (s.position_ms > 0 OR s.position_text IS NOT NULL) AND i.kind `+reading+fmt.Sprintf(" (%d, %d, %d) AND ", KindBook, KindComic, KindIssue)+
		acc.visible("i")+`
		AND (@library = '' OR i.library_id = (SELECT id FROM libraries WHERE public_id = @library))
		ORDER BY s.last_played_at DESC LIMIT `+fmt.Sprint(homeRowSize),
		append(v.args(), sql.Named("since", time.Now().Add(-continueWindow).UnixMilli()), sql.Named("library", library))...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if reading == "NOT IN" {
		if items, err = continueNext(ctx, tx, v, acc, library, items); err != nil {
			return nil, err
		}
	}
	return items, loadImages(ctx, tx, items)
}

// continueNext offers the next episode between plays. Candidates come from the profile's
// watched episodes, without an expiry: a season may arrive years after the last play.
// Each next episode is one seek in that show's episode order.
func continueNext(ctx context.Context, tx *sql.Tx, v Viewer, acc access, library string, items []Item) ([]Item, error) {
	type recentEpisode struct{ root, last, season, number, at int64 }
	recent, err := queryAll(ctx, tx, `SELECT root, last, season, number, at FROM (
		SELECT e.root_id AS root, e.id AS last, ifnull(e.parent_index, 0) AS season,
			ifnull(e.index_number, 0) AS number, p.last_played_at AS at,
			row_number() OVER (PARTITION BY e.root_id ORDER BY p.last_played_at DESC,
				e.parent_index DESC, e.index_number DESC, e.id DESC) AS chosen
		FROM user_item_state p CROSS JOIN items e ON e.id = p.item_id
		WHERE p.profile_id = @viewer_profile AND p.played = 1 AND p.last_played_at IS NOT NULL
			AND p.last_played_at > ifnull(p.continue_dismissed_at, 0)
			AND e.kind = @episode AND e.root_id IS NOT NULL AND `+acc.visible("e")+`
			AND (@library = '' OR e.library_id = (SELECT id FROM libraries WHERE public_id = @library))
			AND NOT EXISTS (SELECT 1 FROM user_item_state done
				WHERE done.profile_id = @viewer_profile AND done.item_id = e.root_id AND done.played = 1)
		) WHERE chosen = 1 ORDER BY at DESC`,
		append(v.args(), sql.Named("episode", KindEpisode), sql.Named("library", library)),
		func(e *recentEpisode) []any { return []any{&e.root, &e.last, &e.season, &e.number, &e.at} })
	if err != nil {
		return nil, err
	}
	seen, activity := map[int64]bool{}, map[int64]int64{}
	for _, item := range items {
		activity[item.ID] = item.State.LastPlayedAt
		if item.Kind == KindEpisode {
			seen[item.RootID] = true
		}
	}
	if len(recent) == 0 {
		return items, nil
	}
	nextEpisode, err := tx.PrepareContext(ctx, `SELECT `+itemColumns(acc)+`, @at > ifnull(s.continue_dismissed_at, 0)`+itemFrom+`
            i.root_id = @root AND i.kind = @episode
			AND (ifnull(i.parent_index, 0), ifnull(i.index_number, 0), i.id) > (@season, @number, @last)
			AND (@season = 0 OR ifnull(i.parent_index, 0) > 0) AND `+acc.visible("i")+`
            AND ifnull(s.played, 0) = 0
			ORDER BY i.parent_index, i.index_number, i.id LIMIT 1`)
	if err != nil {
		return nil, err
	}
	defer nextEpisode.Close()
	for _, previous := range recent {
		if seen[previous.root] {
			continue
		}
		var next Item
		var notDismissed bool
		err := scanItem(nextEpisode.QueryRowContext(ctx,
			append(v.args(), sql.Named("root", previous.root), sql.Named("episode", KindEpisode), sql.Named("at", previous.at),
				sql.Named("season", previous.season), sql.Named("number", previous.number), sql.Named("last", previous.last))...), &next, &notDismissed)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if notDismissed {
			items = append(items, next)
			activity[next.ID] = previous.at
		}
	}
	slices.SortFunc(items, func(a, b Item) int {
		return cmp.Or(cmp.Compare(activity[b.ID], activity[a.ID]), cmp.Compare(a.ID, b.ID))
	})
	return items[:min(len(items), homeRowSize)], nil
}

// RecentlyAdded lists the newest titles across the libraries the viewer may see: films and
// books by when they were added, shows and albums by their newest episode or track, so a show
// with a new episode is new again. It reads the newest few hundred playable items of each
// library through the "date added" index, whatever the size of the library.
func (d *DB) RecentlyAdded(ctx context.Context, v Viewer) ([]Item, error) {
	return d.RecentlyAddedIn(ctx, v, "")
}

// RecentlyAddedIn is RecentlyAdded for one library, by its public id: that library's landing
// page. "" is every library.
func (d *DB) RecentlyAddedIn(ctx context.Context, v Viewer, library string) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	type newest struct {
		id      int64
		addedAt int64
	}
	libraries, err := ids(ctx, tx, `SELECT l.id FROM libraries l WHERE `+acc.libraryVisible("l.id")+` AND (? = '' OR l.public_id = ?)`,
		library, library)
	if err != nil {
		return nil, err
	}
	var titles []newest
	seen := map[int64]bool{}
	for _, leaf := range []struct {
		kind  ItemKind
		title string // the column that names the title a leaf belongs to
	}{{KindMovie, "i.id"}, {KindEpisode, "ifnull(i.root_id, i.id)"}, {KindTrack, "ifnull(i.parent_id, i.id)"}, {KindBook, "i.id"},
		{KindAudiobook, "i.id"}, {KindComic, "i.id"}, {KindIssue, "ifnull(i.parent_id, i.id)"}} {
		for _, library := range libraries {
			from := "items i"
			rated, err := acc.ratedIn(ctx, tx, library, fmt.Sprintf("(%d)", leaf.kind))
			if err != nil {
				return nil, err
			}
			if rated == 0 {
				continue
			}
			may := acc.visible("i")
			if rated < sparseRated {
				from, may = "items i INDEXED BY items_by_rating", "(1"+acc.rated("i", "")+acc.unblocked("i")+")"
			}
			rows, err := queryAll(ctx, tx, `SELECT `+leaf.title+`, i.added_at FROM `+from+` WHERE i.library_id = ? AND i.kind = ? AND `+
				may+` ORDER BY i.added_at DESC, i.id DESC LIMIT 400`, []any{library, leaf.kind},
				func(n *newest) []any { return []any{&n.id, &n.addedAt} })
			if err != nil {
				return nil, err
			}
			inLibrary := 0
			for _, row := range rows {
				if !seen[row.id] && inLibrary < homeRowSize {
					seen[row.id] = true
					inLibrary++
					titles = append(titles, row)
				}
			}
		}
	}
	slices.SortStableFunc(titles, func(a, b newest) int { return cmp.Compare(b.addedAt, a.addedAt) })
	titles = titles[:min(len(titles), homeRowSize)]
	if len(titles) == 0 {
		return nil, nil
	}
	list := make([]int64, len(titles))
	place := map[int64]int{}
	for n, title := range titles {
		list[n], place[title.id] = title.id, n
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns(acc)+itemFrom+`i.id IN `+intList(list)+` AND `+acc.visible("i"), v.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		if err := scanItem(rows, &it); err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Item) int { return cmp.Compare(place[a.ID], place[b.ID]) })
	if err := loadImages(ctx, tx, items); err != nil {
		return nil, err
	}
	return items, loadLeafCounts(ctx, tx, v, acc, items)
}

// HomeView is a saved view put on Home: its name, its library and its sort and filters.
type HomeView struct {
	PublicID, Name, Library, Query string
}

// HomeViews reads the saved views with these ids that the viewer may read, in no particular
// order. One that was deleted, is not a saved view, or is of a library the viewer may no longer
// see is left out, so Home never names it.
func (d *DB) HomeViews(ctx context.Context, v Viewer, publicIDs []string) ([]HomeView, error) {
	if len(publicIDs) == 0 {
		return nil, nil
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
	args, marks := v.args(), make([]string, len(publicIDs))
	for n, id := range publicIDs {
		marks[n] = fmt.Sprint("@view", n)
		args = append(args, sql.Named(fmt.Sprint("view", n), id))
	}
	return queryAll(ctx, tx, `SELECT l.public_id, l.name, ifnull((SELECT public_id FROM libraries WHERE id = l.library_id), ''),
		ifnull(l.rules, '') FROM lists l WHERE l.kind = '`+ListSavedView+`' AND l.public_id IN (`+strings.Join(marks, ", ")+`) AND `+acc.readable(),
		args, func(h *HomeView) []any { return []any{&h.PublicID, &h.Name, &h.Library, &h.Query} })
}
