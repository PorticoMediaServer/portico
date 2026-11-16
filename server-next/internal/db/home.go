package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Home rows the server composes.
const (
	continueWindow = 16 * 7 * 24 * time.Hour // a title untouched for longer drops off Continue Watching
	homeRowSize    = 40
)

// What each of Home's Continue rows holds (definition area 5, rules 37 and 38; decision Q24,
// 21 Oct): films and episodes; music; books, comics and audiobooks.
var (
	watchingKinds  = []ItemKind{KindMovie, KindEpisode}
	listeningKinds = []ItemKind{KindTrack}
	bookKinds      = []ItemKind{KindBook, KindComic, KindIssue, KindAudiobook}
)

// ContinueWatching lists the films and episodes the viewer has started and not finished, and
// the next episode of shows being watched, most recent first. A title the viewer removed from
// the row stays off it until it is played again. Not interested does not take a title off it:
// that is for recommendation rows. It covers every library: Home's row.
func (d *DB) ContinueWatching(ctx context.Context, v Viewer) ([]Item, error) {
	return d.continueRow(ctx, v, watchingKinds, "")
}

// ContinueListening is Home's Continue Listening: the songs the viewer has started and not
// finished, most recent first.
func (d *DB) ContinueListening(ctx context.Context, v Viewer) ([]Item, error) {
	return d.continueRow(ctx, v, listeningKinds, "")
}

// ContinueReading lists the books, comics, issues and audiobooks the viewer has a place in and
// has not finished, most recent first: Home's Continue your books.
func (d *DB) ContinueReading(ctx context.Context, v Viewer) ([]Item, error) {
	return d.continueRow(ctx, v, bookKinds, "")
}

// ContinueIn is everything in progress in one library, by its public id: that library's
// landing page, whatever its kind.
func (d *DB) ContinueIn(ctx context.Context, v Viewer, library string) ([]Item, error) {
	return d.continueRow(ctx, v, nil, library)
}

// continueRow lists what is in progress among kinds (nil: any kind) in library ("": every one).
func (d *DB) continueRow(ctx context.Context, v Viewer, kinds []ItemKind, library string) ([]Item, error) {
	return d.continueItems(ctx, v, kinds, library, homeRowSize)
}

// continueItems reads this profile's started titles; limit < 0 reads its complete Continue source for See all.
func (d *DB) continueItems(ctx context.Context, v Viewer, kinds []ItemKind, library string, limit int) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	within := ""
	if library != "" {
		libraries, err := librariesInScope(ctx, tx, acc, library)
		if err != nil {
			return nil, err
		}
		within = " AND i.library_id IN " + intList(libraries)
	}
	// Read from the viewer's own recent state, newest first, and stop at a row's worth.
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns(acc)+` FROM user_item_state s CROSS JOIN items i ON i.id = s.item_id
		WHERE s.profile_id = @viewer_profile AND (s.last_played_at > @since OR i.kind = `+fmt.Sprintf("%d", KindEpisode)+`) AND s.played = 0 AND s.last_played_at > ifnull(s.continue_dismissed_at, 0)
		AND (s.position_ms > 0 OR s.position_text IS NOT NULL) AND `+kindIn(kinds)+
		acc.visible("i")+within+`
		ORDER BY s.last_played_at DESC LIMIT `+fmt.Sprint(limit),
		append(v.args(), sql.Named("since", time.Now().Add(-continueWindow).UnixMilli()))...)
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
	if slices.Contains(kinds, KindAudiobook) {
		if err := chaptersAt(ctx, tx, items); err != nil {
			return nil, err
		}
	}
	if kinds == nil || slices.Contains(kinds, KindEpisode) {
		if items, err = continueNext(ctx, tx, v, acc, library, items, limit, 0); err != nil {
			return nil, err
		}
	}
	return items, loadImages(ctx, tx, items)
}

// continueNext offers the next episode between plays. Candidates come from the profile's
// watched episodes, without an expiry: a season may arrive years after the last play.
// Each next episode is one seek in that show's episode order.
// limit is -1 for all of Home's source; show is 0 for Home or one show's detail page.
// A show's play target ignores Home dismissal.
func continueNext(ctx context.Context, tx *sql.Tx, v Viewer, acc access, library string, items []Item, limit int, show int64) ([]Item, error) {
	scope := ` AND p.last_played_at > ifnull(p.continue_dismissed_at, 0)`
	whole := ` AND NOT EXISTS (SELECT 1 FROM user_item_state done WHERE done.profile_id = @viewer_profile AND done.item_id = e.root_id AND done.played = 1)`
	dismissed := `@at > ifnull(s.continue_dismissed_at, 0)`
	from := `user_item_state p CROSS JOIN items e ON e.id = p.item_id`
	if show != 0 {
		scope = fmt.Sprintf(" AND e.root_id = %d", show)
		from = `items e CROSS JOIN user_item_state p ON p.item_id=e.id`
		whole = ""
		dismissed = "1"
	}

	within := ""
	if library != "" {
		libraries, err := librariesInScope(ctx, tx, acc, library)
		if err != nil {
			return nil, err
		}
		within = " AND e.library_id IN " + intList(libraries)
	}
	type recentEpisode struct{ root, last, season, number, at int64 }
	recent, err := queryAll(ctx, tx, `SELECT root, last, season, number, at FROM (
		SELECT e.root_id AS root, e.id AS last, ifnull(e.parent_index, 0) AS season,
			ifnull(e.index_number, 0) AS number, p.last_played_at AS at,
			row_number() OVER (PARTITION BY e.root_id ORDER BY p.last_played_at DESC,
				e.parent_index DESC, e.index_number DESC, e.id DESC) AS chosen
		FROM `+from+`
		WHERE p.profile_id = @viewer_profile AND p.played = 1 AND p.last_played_at IS NOT NULL
			`+scope+`
			AND e.kind = @episode AND e.root_id IS NOT NULL AND `+acc.visible("e")+within+whole+`
		) WHERE chosen = 1 ORDER BY at DESC`,
		append(v.args(), sql.Named("episode", KindEpisode)),
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
	nextEpisode, err := tx.PrepareContext(ctx, `SELECT `+itemColumns(acc)+`, `+dismissed+itemFrom+`
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
	if limit >= 0 {
		items = items[:min(len(items), limit)]
	}
	return items, nil
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
		id, leafID, addedAt int64
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
			inLibrary := 0
			seek := ""
			args := []any{library, leaf.kind}
			for inLibrary < homeRowSize {
				rows, err := queryAll(ctx, tx, `SELECT `+leaf.title+`, i.id, i.added_at FROM `+from+` WHERE i.library_id=? AND i.kind=? AND `+
					may+seek+` ORDER BY i.added_at DESC,i.id DESC LIMIT 400`, args,
					func(n *newest) []any { return []any{&n.id, &n.leafID, &n.addedAt} })
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					if !seen[row.id] && inLibrary < homeRowSize {
						seen[row.id] = true
						inLibrary++
						titles = append(titles, row)
					}
				}
				if len(rows) < 400 || inLibrary == homeRowSize {
					break
				}
				last := rows[len(rows)-1]
				seek = " AND (i.added_at,i.id)<(?,?)"
				args = []any{library, leaf.kind, last.addedAt, last.leafID}
			}
		}
	}
	slices.SortStableFunc(titles, func(a, b newest) int { return cmp.Or(cmp.Compare(b.addedAt, a.addedAt), cmp.Compare(b.id, a.id)) })
	titles = titles[:min(len(titles), homeRowSize)]
	if len(titles) == 0 {
		return nil, nil
	}
	list := make([]int64, len(titles))
	for n, title := range titles {
		list[n] = title.id
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns(acc)+`, max(i.added_at,ifnull(i.last_child_added_at,0))`+itemFrom+`i.id IN `+intList(list)+` AND `+acc.visible("i"), v.args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []Item
	for rows.Next() {
		var it Item
		var added int64
		if err := scanItem(rows, &it, &added); err != nil {
			return nil, err
		}
		it.sortValues = []any{added}
		items = append(items, it)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(items, func(a, b Item) int {
		return cmp.Or(cmp.Compare(b.sortValues[0].(int64), a.sortValues[0].(int64)), cmp.Compare(b.ID, a.ID))
	})
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

// kindIn is the condition that a title is one of kinds, or always true for nil (any kind).
func kindIn(kinds []ItemKind) string {
	list := make([]string, len(kinds))
	for n, kind := range kinds {
		list[n] = strconv.Itoa(int(kind))
	}
	if kinds == nil {
		return "1 AND "
	}
	return "i.kind IN (" + strings.Join(list, ", ") + ") AND "
}

// Chapter is a chapter of a title's file, numbered from 1 through the whole file.
type Chapter struct {
	Number         int
	Title          string
	StartMS, EndMS int64
}

// chaptersAt gives each audiobook of a Continue row the chapter its place is in, so its card can
// say "Chapter 5 · 9 h 12 min left" (tablet-read frame 11). A book of several files, or a place
// outside every chapter, has none: the card then says only the time left.
func chaptersAt(ctx context.Context, tx *sql.Tx, items []Item) error {
	for n := range items {
		it := &items[n]
		if it.Kind != KindAudiobook || it.State.PositionMS <= 0 {
			continue
		}
		var c Chapter
		err := tx.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM media_markers o WHERE o.media_id = k.media_id AND o.kind = ?1
				AND o.start_ms <= k.start_ms), ifnull(k.title, ''), k.start_ms, k.end_ms
			FROM media m JOIN media_markers k ON k.media_id = m.id
			WHERE m.item_id = ?2 AND k.kind = ?1 AND k.start_ms <= ?3 AND k.end_ms > ?3
				AND (SELECT count(*) FROM media_parts p WHERE p.media_id = m.id) = 1
			ORDER BY m.id, k.start_ms DESC LIMIT 1`, MarkerChapter, it.ID, it.State.PositionMS).Scan(&c.Number, &c.Title, &c.StartMS, &c.EndMS)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		it.Chapter = &c
	}
	return nil
}

// RecentlyAddedCursor continues the indexed Home preview in the ordinary row list's order.
func RecentlyAddedCursor(items []Item) string {
	if len(items) < homeRowSize {
		return ""
	}
	last := items[len(items)-1]
	return (listCursor{Sort: SortAdded, Desc: true, Values: last.sortValues, ID: last.ID}).encode()
}
