package db

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"
)

// Home rows the server composes.
const (
	continueWindow = 16 * 7 * 24 * time.Hour // a title untouched for longer drops off a Continue row
	continueSize   = 40                      // the most a Continue row holds
	continueRead   = 400                     // how far back in the viewer's own plays a Continue row looks
)

// The three Continue rows, by what they hold.
var (
	ContinueWatching  = []ItemKind{KindMovie, KindEpisode}
	ContinueListening = []ItemKind{KindTrack, KindAudiobook}
	ContinueReading   = []ItemKind{KindBook, KindComic, KindIssue}
)

// Continue lists what the viewer has started and not finished among the given kinds, most
// recent first, and for a show whose last episode they finished, the episode after it. With
// library set (a public id) it holds only that library's titles.
//
// It reads the viewer's own most recent plays, never the library, so it costs the same in a
// library of any size.
func (d *DB) Continue(ctx context.Context, v Viewer, kinds []ItemKind, library string) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	in := ""
	if library != "" {
		in = ` AND i.library_id = (SELECT id FROM libraries WHERE public_id = @library)`
	}
	type played struct {
		id, root    int64
		kind        ItemKind
		season      int
		episode     int
		done, shown bool
	}
	recent, err := queryAll(ctx, tx, `SELECT i.id, ifnull(i.root_id, 0), i.kind, ifnull(i.parent_index, 0), ifnull(i.index_number, 0), s.played,
		s.played = 0 AND s.hidden = 0 AND (s.position_ms > 0 OR s.position_text IS NOT NULL)
		FROM user_item_state s CROSS JOIN items i ON i.id = s.item_id
		WHERE s.profile_id = @viewer_profile AND s.last_played_at > @since AND i.kind IN `+kindList(kinds)+in+` AND `+acc.visible("i")+`
		ORDER BY s.last_played_at DESC LIMIT `+fmt.Sprint(continueRead),
		append(v.args(), sql.Named("since", time.Now().Add(-continueWindow).UnixMilli()), sql.Named("library", library)),
		func(p *played) []any { return []any{&p.id, &p.root, &p.kind, &p.season, &p.episode, &p.done, &p.shown} })
	if err != nil {
		return nil, err
	}
	var list []int64
	shows := map[int64]bool{}
	for _, p := range recent {
		if len(list) == continueSize {
			break
		}
		// A show is on the row once, by the most recent thing done in it.
		if p.kind == KindEpisode && p.root != 0 {
			if shows[p.root] {
				continue
			}
			shows[p.root] = true
		}
		switch {
		case p.shown:
			list = append(list, p.id)
		case p.done && p.kind == KindEpisode && p.root != 0:
			// The episode after the one just finished, if the viewer may see it and has not
			// watched or dismissed it. Specials (season 0) follow only a special.
			var next int64
			err := tx.QueryRowContext(ctx, `SELECT n.id FROM items n WHERE n.root_id = @root AND n.kind = @kind
				AND (ifnull(n.parent_index, 0), ifnull(n.index_number, 0)) > (@season, @episode) AND (@season = 0 OR ifnull(n.parent_index, 0) > 0)
				AND `+acc.visible("n")+` ORDER BY n.parent_index, n.index_number, n.id LIMIT 1`,
				sql.Named("root", p.root), sql.Named("kind", KindEpisode), sql.Named("season", p.season), sql.Named("episode", p.episode)).Scan(&next)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return nil, err
			}
			var skip bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM user_item_state WHERE profile_id = ? AND item_id = ? AND (played = 1 OR hidden = 1))`,
				v.ProfileID, next).Scan(&skip); err != nil {
				return nil, err
			}
			if !skip {
				list = append(list, next)
			}
		}
	}
	return itemsInOrder(ctx, tx, v, acc, list)
}

func kindList(kinds []ItemKind) string {
	ids := make([]int64, len(kinds))
	for n, kind := range kinds {
		ids[n] = int64(kind)
	}
	return intList(ids)
}

// itemsInOrder reads the items with these ids that the viewer may see, in the order given.
func itemsInOrder(ctx context.Context, tx *sql.Tx, v Viewer, acc access, list []int64) ([]Item, error) {
	if len(list) == 0 {
		return nil, nil
	}
	place := map[int64]int{}
	for n, id := range list {
		place[id] = n
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+itemFrom+`i.id IN `+intList(list)+` AND `+acc.visible("i"), v.args()...)
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

// recentCursor is where the next page of a Recently Added row starts: the newest file of the
// last title sent.
type recentCursor struct {
	At int64 `json:"a"`
	ID int64 `json:"i"`
}

// recentChunk is how many files a Recently Added row reads at a time, and recentChunks how
// many times it will before it sends what it has; the next page carries on from there.
const (
	recentChunk  = 500
	recentChunks = 20
)

// RecentlyAdded lists one library's titles, the most recently added first: films and books by
// when they were added, shows and albums by their newest episode or track that the viewer may
// see, so a show with a new episode is new again. It pages until the library runs out.
//
// It walks the library's playable items through the "date added" index, newest first, and
// keeps the title each belongs to the first time it meets it, so a page costs what it reads
// and not the size of the library.
func (d *DB) RecentlyAdded(ctx context.Context, v Viewer, library, cursor string, limit int) (ItemPage, error) {
	var page ItemPage
	var after *recentCursor
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || json.Unmarshal(raw, &after) != nil || after == nil {
			return page, invalid("the cursor does not belong to this row")
		}
	}
	if limit <= 0 {
		limit = defaultPageSize
	}
	limit = min(limit, maxPageSize)
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return page, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return page, err
	}
	var libraryID int64
	var kind string
	err = tx.QueryRowContext(ctx, `SELECT l.id, l.kind FROM libraries l WHERE l.public_id = ? AND `+acc.libraryVisible("l.id"), library).Scan(&libraryID, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return page, ErrNotFound
	}
	if err != nil {
		return page, err
	}
	// The playable kind a library of this kind holds, and the column that names the title
	// each belongs to: a film is its own title, an episode's is its show, a track's its album.
	leaf, title, under := KindMovie, "i.id", ""
	switch kind {
	case LibraryShows:
		leaf, title, under = KindEpisode, "ifnull(i.root_id, i.id)", "i.root_id"
	case LibraryMusic:
		leaf, title, under = KindTrack, "ifnull(i.parent_id, i.id)", "i.parent_id"
	case LibraryBooks:
		leaf = KindBook
	case LibraryAudiobooks:
		leaf = KindAudiobook
	}
	type file struct{ title, at, id int64 }
	var list []int64
	seen := map[int64]bool{}
	// at is the last file read; last is the file that put the page's last title on it.
	at, last := after, recentCursor{}
	full, ended := false, false
read:
	for range recentChunks {
		where, args := "", []any{libraryID, leaf}
		if at != nil {
			where, args = ` AND (i.added_at, i.id) < (?, ?)`, append(args, at.At, at.ID)
		}
		files, err := queryAll(ctx, tx, `SELECT `+title+`, i.added_at, i.id FROM items i WHERE i.library_id = ? AND i.kind = ? AND `+acc.visible("i")+where+`
			ORDER BY i.added_at DESC, i.id DESC LIMIT `+fmt.Sprint(recentChunk), args, func(f *file) []any { return []any{&f.title, &f.at, &f.id} })
		if err != nil {
			return page, err
		}
		for _, f := range files {
			if seen[f.title] {
				at = &recentCursor{f.at, f.id}
				continue
			}
			if after != nil && f.title != f.id {
				// A title with a file as new as where this page starts, or newer, was sent on an earlier page.
				var sent bool
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM items i WHERE `+under+` = ? AND i.kind = ?
					AND (i.added_at, i.id) >= (?, ?) AND `+acc.visible("i")+`)`, f.title, leaf, after.At, after.ID).Scan(&sent); err != nil {
					return page, err
				}
				if sent {
					seen[f.title], at = true, &recentCursor{f.at, f.id}
					continue
				}
			}
			if len(list) == limit {
				full = true
				break read
			}
			seen[f.title], at = true, &recentCursor{f.at, f.id}
			list, last = append(list, f.title), *at
		}
		if len(files) < recentChunk {
			ended = true
			break
		}
	}
	// A page that ran out of reading before it filled carries on after the last file read.
	if !full && !ended && at != nil {
		last = *at
	}
	more := full || !ended
	if page.Items, err = itemsInOrder(ctx, tx, v, acc, list); err != nil {
		return page, err
	}
	if more {
		raw, _ := json.Marshal(last)
		page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
	}
	return page, nil
}

// homeLayoutKey is the preference a profile's Home layout is kept under: its own, the same
// on every device.
const homeLayoutKey = "home.layout"

// HomeLayout returns the layout a profile saved as JSON, or "" when it never saved one.
func (d *DB) HomeLayout(ctx context.Context, profileID int64) (string, error) {
	var value string
	err := d.read.QueryRowContext(ctx, `SELECT value FROM preferences WHERE profile_id = ? AND device_class = '' AND device_id IS NULL AND key = ?`,
		profileID, homeLayoutKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}

// SetHomeLayout saves a profile's Home layout, or with "" goes back to the default. The later
// save wins. The profile's other open apps are told.
func (d *DB) SetHomeLayout(ctx context.Context, profileID int64, layout string, now time.Time) error {
	if layout == "" {
		return d.SetPreferences(ctx, profileID, "", 0, nil, []string{homeLayoutKey}, now)
	}
	return d.SetPreferences(ctx, profileID, "", 0, map[string]string{homeLayoutKey: layout}, nil, now)
}
