package db

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Mood rows: Home rows of titles sharing a mood or theme the profile likes ("Cozy stories for
// you"), each saying which titles it comes from. Every read keeps to what the profile may see:
// a title it may not see is never offered, named or counted towards a liking.
//
// Choosing the rows reads the profile's taste and up to 2,000 titles per tag, too slow for every
// Home request in a large library. So the choice is kept in memory per profile, as title ids,
// and made again in the background (KeepMoodRows) when the profile's plays, favourites,
// ratings or restrictions change, when the title data's tags change, and when it is an hour
// old. Home reads the kept ids through the same visibility and not-watched conditions as
// everything else, so a new rating limit or blocked label applies on the very next read.
const (
	tasteRecent    = 500  // the profile's most recently played titles read for its taste
	tasteFavorites = 100  // and its most recent favourites
	moodLiked      = 3    // titles with a tag the profile must have watched or liked to get its row
	moodRowsMax    = 3    // rows on Home
	moodRowMin     = 6    // a row with fewer titles is left out
	moodScan       = 2000 // newest titles with a tag read for its row
	moodBecause    = 5    // titles kept per row to say why; two are named

	moodKeptFor = time.Hour       // a choice older than this is made again when Home is read
	moodIdle    = 24 * time.Hour  // a choice Home has not read for this long is dropped, not remade
	moodFollow  = 5 * time.Second // how often the changes are read for stale choices
)

// MoodRow is one Home row of titles carrying a tag from the title data.
type MoodRow struct {
	Key     string       // the tag's key in the data set, "mood:cozy"
	Label   string       // its row label, "cozy stories"
	Because []MoodReason // up to two titles the profile watched or liked that carry it, newest first
	Items   []Item
}

// MoodReason is a title a mood row comes from, and whether the profile watched it (played or
// started it) or only liked it.
type MoodReason struct {
	Item    Item
	Watched bool
}

// keptMoodRow is a mood row as chosen, by title id.
type keptMoodRow struct {
	key, label string
	because    []int64 // newest first
	watched    map[int64]bool
	items      []int64 // best first, more than a row shows, so titles hidden since leave enough
}

type moodChoice struct {
	viewer Viewer
	rows   []keptMoodRow
	made   time.Time
	asked  time.Time
	stale  bool
}

// moodKeeper holds each profile's chosen mood rows, and which profiles Home asked for whose
// choice is missing or stale.
type moodKeeper struct {
	mu       sync.Mutex
	choices  map[int64]*moodChoice
	wanted   map[int64]Viewer
	wake     chan struct{}
	cursor   int64 // the last change read
	followed bool
}

// ask answers the kept choice (possibly stale, or none) and, unless it is fresh, asks for a new
// one.
func (k *moodKeeper) ask(v Viewer) []keptMoodRow {
	k.mu.Lock()
	defer k.mu.Unlock()
	c := k.choices[v.ProfileID]
	if c != nil {
		c.asked = time.Now()
	}
	if c != nil && !c.stale && time.Since(c.made) < moodKeptFor {
		return c.rows
	}
	if k.wanted == nil {
		k.wanted = map[int64]Viewer{}
	}
	k.wanted[v.ProfileID] = v
	if k.wake == nil {
		k.wake = make(chan struct{}, 1)
	}
	select {
	case k.wake <- struct{}{}:
	default:
	}
	if c == nil {
		return nil
	}
	return c.rows
}

func (k *moodKeeper) waker() chan struct{} {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.wake == nil {
		k.wake = make(chan struct{}, 1)
	}
	return k.wake
}

// stale has one profile's choice made again, or every profile's with 0, by the next
// RefreshMoodRows, so Home's next read has it. A choice Home has not read for a day is dropped
// instead.
func (k *moodKeeper) stale(profileID int64) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, c := range k.choices {
		if profileID != 0 && id != profileID {
			continue
		}
		if time.Since(c.asked) > moodIdle {
			delete(k.choices, id)
			continue
		}
		c.stale = true
		if k.wanted == nil {
			k.wanted = map[int64]Viewer{}
		}
		k.wanted[id] = c.viewer
	}
}

func (k *moodKeeper) next() (Viewer, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, v := range k.wanted {
		delete(k.wanted, id)
		return v, true
	}
	return Viewer{}, false
}

func (k *moodKeeper) keep(v Viewer, rows []keptMoodRow) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.choices == nil {
		k.choices = map[int64]*moodChoice{}
	}
	now := time.Now()
	asked := now
	if old := k.choices[v.ProfileID]; old != nil {
		asked = old.asked // a remake is not Home asking
	}
	k.choices[v.ProfileID] = &moodChoice{viewer: v, rows: rows, made: now, asked: asked}
}

// KeepMoodRows makes the mood rows Home asks for, in the background, until ctx ends.
func (d *DB) KeepMoodRows(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(moodFollow)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-d.moods.waker():
		}
		if err := d.RefreshMoodRows(ctx); err != nil && ctx.Err() == nil {
			log.Warn("choosing mood rows failed; they are tried again shortly", "error", err)
		}
	}
}

// RefreshMoodRows reads the changes since it last ran, marks the choices they touch stale, and
// makes every choice Home asked for since and every stale one. KeepMoodRows runs it; a test
// can run it directly. One caller at a time.
func (d *DB) RefreshMoodRows(ctx context.Context) error {
	k := &d.moods
	if !k.followed {
		head, err := d.ChangesHead(ctx)
		if err != nil {
			return err
		}
		k.cursor, k.followed = head, true // nothing is kept yet, so nothing before now matters
	}
	for {
		changes, reread, head, err := d.Changes(ctx, k.cursor, 1000)
		if err != nil {
			return err
		}
		if reread {
			k.stale(0)
			k.cursor = head
			break
		}
		for _, c := range changes {
			// A play, favourite, rating or Not interested changes the taste; a restriction
			// changes what may count towards it.
			if c.Topic == TopicState || c.Topic == TopicAccess {
				k.stale(c.ProfileID)
			}
			k.cursor = c.ID
		}
		if len(changes) < 1000 {
			break
		}
	}
	for {
		v, ok := k.next()
		if !ok {
			return nil
		}
		rows, err := d.chooseMoodRows(ctx, v)
		if err != nil {
			k.mu.Lock()
			k.wanted[v.ProfileID] = v // try again on the next round
			k.mu.Unlock()
			return err
		}
		k.keep(v, rows)
	}
}

// MoodRows answers the profile's mood rows from its kept choice, read now through what it may
// see, leaving out what it has watched or hidden since. With no choice kept yet, or a stale one,
// it asks for a new one and Home shows it on its next read. With no title data in use there
// are none.
func (d *DB) MoodRows(ctx context.Context, v Viewer) ([]MoodRow, error) {
	if d.titleData.Load() == nil {
		return nil, nil
	}
	kept := d.moods.ask(v)
	if len(kept) == 0 {
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
	var out []MoodRow
	offered := map[int64]bool{}
	for _, k := range kept {
		because, err := itemsByID(ctx, tx, v, acc, k.because, "")
		if err != nil {
			return nil, err
		}
		if len(because) == 0 {
			continue // every title it came from is hidden now
		}
		items, err := itemsByID(ctx, tx, v, acc, k.items, ` AND ifnull(s.played, 0) = 0 AND ifnull(s.hidden, 0) = 0`)
		if err != nil {
			return nil, err
		}
		row := MoodRow{Key: k.key, Label: k.label}
		for _, it := range items {
			if !offered[it.ID] && len(row.Items) < homeRowSize {
				row.Items = append(row.Items, it)
			}
		}
		if len(row.Items) < moodRowMin {
			continue
		}
		for _, it := range row.Items {
			offered[it.ID] = true
		}
		for _, it := range because[:min(2, len(because))] {
			row.Because = append(row.Because, MoodReason{Item: it, Watched: k.watched[it.ID]})
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

// chooseMoodRows chooses the profile's mood rows: the tags most common among the films and shows
// it played or favourited recently (a title marked watched by hand without playing is not
// read: only plays and favourites have an index by time), each in at least three of them,
// strongest first, and for each the best-rated titles with that tag it has not watched, rated
// titles first and then the newest. A title is in one row at most.
func (d *DB) chooseMoodRows(ctx context.Context, v Viewer) ([]keptMoodRow, error) {
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
		id      int64
		at      int64
		watched bool
	}
	taste, err := queryAll(ctx, tx, `SELECT t.id, max(max(ifnull(s.last_played_at, 0), ifnull(s.favorite_at, 0))) AS at,
			max(s.played = 1 OR s.last_played_at IS NOT NULL OR ifnull(s.position_ms, 0) > 0)
		FROM (SELECT item_id FROM (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND last_played_at IS NOT NULL
				ORDER BY last_played_at DESC LIMIT `+fmt.Sprint(tasteRecent)+`)
			UNION SELECT item_id FROM (SELECT item_id FROM user_item_state WHERE profile_id = @viewer_profile AND favorite = 1
				ORDER BY favorite_at DESC LIMIT `+fmt.Sprint(tasteFavorites)+`)) r
		CROSS JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = r.item_id
		CROSS JOIN items i ON i.id = s.item_id CROSS JOIN items t ON t.id = ifnull(i.root_id, i.id)
		WHERE s.hidden = 0 AND ifnull(s.rating, 10) > 4 AND (s.played = 1 OR s.favorite = 1 OR s.position_ms > 0)
		AND t.kind IN `+intList([]int64{int64(KindMovie), int64(KindShow)})+` AND `+acc.visible("i")+` AND `+acc.visible("t")+`
		GROUP BY t.id ORDER BY at DESC, t.id DESC`, v.args(),
		func(l *tasteTitle) []any { return []any{&l.id, &l.at, &l.watched} })
	if err != nil || len(taste) < moodLiked {
		return nil, err
	}
	tasteIDs := make([]int64, len(taste))
	newest := map[int64]int{}
	watched := map[int64]bool{}
	for n, l := range taste {
		tasteIDs[n], newest[l.id], watched[l.id] = l.id, n, l.watched
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
		used[id] = true // what the profile already watched or liked is not offered
	}
	var out []keptMoodRow
	for _, tc := range liked {
		if len(out) == moodRowsMax {
			break
		}
		label, ok := labels[tc.key]
		if !ok {
			continue // the tag left the vocabulary; the daily pass takes it off
		}
		// With no rating known (no provider match yet, or local metadata only) the newest
		// releases come first.
		ids, err := queryAll(ctx, tx, `SELECT i.id FROM (SELECT item_id FROM item_tags WHERE tag_id = `+fmt.Sprint(tc.id)+`
				ORDER BY item_id DESC LIMIT `+fmt.Sprint(moodScan)+`) m
			CROSS JOIN items i ON i.id = m.item_id LEFT JOIN user_item_state s ON s.profile_id = @viewer_profile AND s.item_id = i.id
			WHERE i.parent_id IS NULL AND ifnull(s.played, 0) = 0 AND ifnull(s.hidden, 0) = 0 AND `+acc.visible("i")+`
			ORDER BY i.community_rating IS NULL, i.community_rating DESC, ifnull(i.year, 0) DESC, i.sort_title, i.id
			LIMIT `+fmt.Sprint(homeRowSize*2), v.args(), func(id *int64) []any { return []any{id} })
		if err != nil {
			return nil, err
		}
		row := keptMoodRow{key: tc.key, label: label, watched: map[int64]bool{}}
		for _, id := range ids {
			if !used[id] {
				row.items = append(row.items, id)
			}
		}
		if len(row.items) < moodRowMin {
			continue
		}
		for _, id := range row.items[:min(homeRowSize, len(row.items))] {
			used[id] = true
		}
		row.because = tc.items[:min(moodBecause, len(tc.items))]
		for _, id := range row.because {
			row.watched[id] = watched[id]
		}
		out = append(out, row)
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

// itemsByID reads items the viewer may see, in the order given, with any further condition
// on the item (i) and the viewer's state (s).
func itemsByID(ctx context.Context, tx *sql.Tx, v Viewer, acc access, list []int64, where string) ([]Item, error) {
	if len(list) == 0 {
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT `+itemColumns+itemFrom+`i.id IN `+intList(list)+` AND `+acc.visible("i")+where, v.args()...)
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
		return nil, d.unlessSwapped(td, err)
	}
	similar, err := td.conn.QueryContext(ctx, `SELECT w.qid, w.provider, w.provider_id FROM work_similar s CROSS JOIN work_ids w ON w.qid = s.similar
		WHERE s.qid = ? ORDER BY s.rank, w.provider`, qid)
	if err != nil {
		return nil, d.unlessSwapped(td, err)
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
		return nil, d.unlessSwapped(td, err)
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

// unlessSwapped is err, or nothing when the title data file it came from was replaced or
// switched off during the read (its connection is closed then): the reader answers as if there
// were no title data rather than failing the page.
func (d *DB) unlessSwapped(td *titleData, err error) error {
	if err != nil && d.titleData.Load() != td {
		return nil
	}
	return err
}
