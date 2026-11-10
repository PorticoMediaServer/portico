package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Mood rows: Home rows of titles sharing a mood or theme the profile likes ("Cozy stories for
// you"), each saying which watched titles it comes from. Every read keeps to what the profile
// may see: a title it may not see is never offered, named or counted towards a liking.
const (
	tasteRecent    = 500  // the profile's most recently played titles read for its taste
	tasteFavorites = 100  // and its most recent favourites
	moodLiked      = 3    // titles with a tag the profile must have watched or liked to get its row
	moodRowsMax    = 3    // rows on Home
	moodRowMin     = 6    // a row with fewer titles is left out
	moodScan       = 2000 // newest titles with a tag read for its row
)

// MoodRow is one Home row of titles carrying a tag from the title data.
type MoodRow struct {
	Key     string // the tag's key in the data set, "mood:cozy"
	Label   string // its row label, "cozy stories"
	Because []Item // up to two titles the profile watched or liked that carry it, newest first
	Items   []Item
}

// MoodRows composes the profile's mood rows: the tags most common among the films and shows it
// played or favourited recently (a title marked watched by hand without playing is not read:
// only plays and favourites have an index by time) (each in at least three of them), strongest first, and
// for each the best-rated titles with that tag it has not watched. A title is in one row at most.
// With no title data in use there are none.
func (d *DB) MoodRows(ctx context.Context, v Viewer) ([]MoodRow, error) {
	td := d.titleData.Load()
	if td == nil {
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
	// The taste: titles (an episode counts as its show) the profile played, started or liked,
	// newest first, read from its two state indexes. Both the item played and the title it
	// belongs to must be visible now; a low rating or Not interested is not a liking.
	type tasteTitle struct {
		id int64
		at int64
	}
	taste, err := queryAll(ctx, tx, `SELECT t.id, max(max(ifnull(s.last_played_at, 0), ifnull(s.favorite_at, 0))) AS at
		FROM (SELECT item_id FROM (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND last_played_at IS NOT NULL
				ORDER BY last_played_at DESC LIMIT `+fmt.Sprint(tasteRecent)+`)
			UNION SELECT item_id FROM (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND favorite = 1
				ORDER BY favorite_at DESC LIMIT `+fmt.Sprint(tasteFavorites)+`)) r
		CROSS JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = r.item_id
		CROSS JOIN items i ON i.id = s.item_id CROSS JOIN items t ON t.id = ifnull(i.root_id, i.id)
		WHERE s.hidden = 0 AND ifnull(s.rating, 10) > 4 AND (s.played = 1 OR s.favorite = 1 OR s.position_ms > 0)
		AND t.kind IN `+intList([]int64{int64(KindMovie), int64(KindShow)})+` AND `+acc.visible("i")+` AND `+acc.visible("t")+`
		GROUP BY t.id ORDER BY at DESC, t.id DESC`, v.args(),
		func(l *tasteTitle) []any { return []any{&l.id, &l.at} })
	if err != nil || len(taste) < moodLiked {
		return nil, err
	}
	tasteIDs := make([]int64, len(taste))
	newest := map[int64]int{}
	for n, l := range taste {
		tasteIDs[n], newest[l.id] = l.id, n
	}
	// How many liked titles carry each mood tag, and which, newest first.
	type carried struct {
		item int64
		tag  int64
		key  string
	}
	carries, err := queryAll(ctx, tx, `SELECT it.item_id, t.id, t.name FROM item_tags it CROSS JOIN tags t ON t.id = it.tag_id
		WHERE it.item_id IN `+intList(tasteIDs)+` AND t.kind = 'mood'`, nil,
		func(c *carried) []any { return []any{&c.item, &c.tag, &c.key} })
	if err != nil {
		return nil, err
	}
	type tagCount struct {
		id    int64
		key   string
		items []int64
	}
	byTag := map[int64]*tagCount{}
	for _, c := range carries {
		if byTag[c.tag] == nil {
			byTag[c.tag] = &tagCount{id: c.tag, key: c.key}
		}
		byTag[c.tag].items = append(byTag[c.tag].items, c.item)
	}
	var liked []*tagCount
	for _, tc := range byTag {
		if len(tc.items) >= moodLiked {
			slices.SortFunc(tc.items, func(a, b int64) int { return cmp.Compare(newest[a], newest[b]) })
			liked = append(liked, tc)
		}
	}
	slices.SortFunc(liked, func(a, b *tagCount) int {
		return cmp.Or(cmp.Compare(len(b.items), len(a.items)), cmp.Compare(a.key, b.key))
	})
	labels, err := td.rowLabels(ctx)
	if err != nil {
		return nil, err
	}
	used := map[int64]bool{}
	for _, id := range tasteIDs {
		used[id] = true // what the profile already watched is not offered
	}
	var out []MoodRow
	for _, tc := range liked {
		if len(out) == moodRowsMax {
			break
		}
		label, ok := labels[tc.key]
		if !ok {
			continue // the tag left the vocabulary; the daily pass takes it off
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM (SELECT item_id FROM item_tags WHERE tag_id = `+fmt.Sprint(tc.id)+`
				ORDER BY item_id DESC LIMIT `+fmt.Sprint(moodScan)+`) m
			CROSS JOIN items i ON i.id = m.item_id LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
			WHERE i.parent_id IS NULL AND ifnull(s.played, 0) = 0 AND ifnull(s.hidden, 0) = 0 AND `+acc.visible("i")+`
			ORDER BY ifnull(i.community_rating, 0) DESC, i.id DESC LIMIT `+fmt.Sprint(homeRowSize*2), v.args()...)
		if err != nil {
			return nil, err
		}
		row := MoodRow{Key: tc.key, Label: label}
		for rows.Next() {
			var it Item
			if err := scanItem(rows, &it); err != nil {
				rows.Close()
				return nil, err
			}
			if !used[it.ID] && len(row.Items) < homeRowSize {
				row.Items = append(row.Items, it)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(row.Items) < moodRowMin {
			continue
		}
		for _, it := range row.Items {
			used[it.ID] = true
		}
		if row.Because, err = itemsByID(ctx, tx, v, acc, tc.items[:min(2, len(tc.items))]); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	for n := range out {
		if err := loadImages(ctx, tx, out[n].Items); err != nil {
			return nil, err
		}
		if err := loadLeafCounts(ctx, tx, v, acc, out[n].Items); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// rowLabels answers each row-making tag's row label in the file.
func (td *titleData) rowLabels(ctx context.Context) (map[string]string, error) {
	rows, err := td.conn.QueryContext(ctx, `SELECT tag, row_label FROM vocabulary WHERE row_label IS NOT NULL AND retired_in IS NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var tag, label string
		if err := rows.Scan(&tag, &label); err != nil {
			return nil, err
		}
		out[tag] = label
	}
	return out, rows.Err()
}

// itemsByID reads items the viewer may see, in the order given.
func itemsByID(ctx context.Context, tx *sql.Tx, v Viewer, acc access, list []int64) ([]Item, error) {
	if len(list) == 0 {
		return nil, nil
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
	slices.SortFunc(items, func(a, b Item) int { return cmp.Compare(slices.Index(list, a.ID), slices.Index(list, b.ID)) })
	return items, rows.Err()
}

// titleDataTarget is how one of the file's provider codes names a title here.
var titleDataTarget = map[int]struct {
	provider string
	kinds    []ItemKind
}{
	1: {"tmdb", []ItemKind{KindMovie}}, 2: {"tmdb", []ItemKind{KindShow}}, 3: {"tvdb", []ItemKind{KindShow}},
	4: {"tvdb", []ItemKind{KindMovie}}, 5: {"anilist", []ItemKind{KindMovie, KindShow}}, 6: {"imdb", []ItemKind{KindMovie, KindShow}},
}

// MoreLikeThis lists the titles the title data names as like a film or show, best first, that
// are in the viewer's libraries and that the viewer may see: up to fifteen. Similar titles are
// read from the file and found here by their provider ids, so a title added or matched later
// joins the row by itself. A title the viewer may not see is not found.
func (d *DB) MoreLikeThis(ctx context.Context, v Viewer, itemID string) ([]Item, error) {
	tx, err := d.read.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	acc, err := loadAccess(ctx, tx, v)
	if err != nil {
		return nil, err
	}
	var id int64
	var kind ItemKind
	err = tx.QueryRowContext(ctx, `SELECT i.id, i.kind FROM items i WHERE i.public_id = ? AND `+acc.visible("i"), itemID).Scan(&id, &kind)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	td := d.titleData.Load()
	if err != nil || td == nil || (kind != KindMovie && kind != KindShow) {
		return nil, err
	}
	type pair struct{ provider, id string }
	ext, err := queryAll(ctx, tx, `SELECT provider, external_id FROM item_external_ids WHERE item_id = ?`, []any{id},
		func(p *pair) []any { return []any{&p.provider, &p.id} })
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	for _, p := range ext {
		ids[p.provider] = p.id
	}
	qid, err := titleDataWork(ctx, td, kind, ids)
	if err != nil || qid == 0 {
		return nil, err
	}
	similar, err := td.conn.QueryContext(ctx, `SELECT w.qid, w.provider, w.provider_id FROM work_similar s CROSS JOIN work_ids w ON w.qid = s.similar
		WHERE s.qid = ? ORDER BY s.rank, w.provider`, qid)
	if err != nil {
		return nil, err
	}
	type target struct {
		qid  int64
		code int
		id   any
	}
	var targets []target
	for similar.Next() {
		var t target
		if err := similar.Scan(&t.qid, &t.code, &t.id); err != nil {
			similar.Close()
			return nil, err
		}
		targets = append(targets, t)
	}
	similar.Close()
	if err := similar.Err(); err != nil {
		return nil, err
	}
	var out []Item
	seen := map[int64]bool{id: true}
	found := map[int64]bool{} // a similar work already found by another of its ids
	for _, t := range targets {
		how, ok := titleDataTarget[t.code]
		if !ok || found[t.qid] {
			continue
		}
		external := strings.ToLower(fmt.Sprint(t.id))
		kinds := make([]int64, len(how.kinds))
		for n, k := range how.kinds {
			kinds[n] = int64(k)
		}
		rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+` FROM item_external_ids x CROSS JOIN items i ON i.id = x.item_id
			LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
			WHERE x.provider = @provider AND x.external_id = @external AND i.parent_id IS NULL AND i.kind IN `+intList(kinds)+` AND `+acc.visible("i")+`
			ORDER BY i.id`, append(v.args(), sql.Named("provider", how.provider), sql.Named("external", external))...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var it Item
			if err := scanItem(rows, &it); err != nil {
				rows.Close()
				return nil, err
			}
			found[t.qid] = true
			if !seen[it.ID] {
				seen[it.ID] = true
				out = append(out, it)
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	if err := loadImages(ctx, tx, out); err != nil {
		return nil, err
	}
	return out, loadLeafCounts(ctx, tx, v, acc, out)
}
