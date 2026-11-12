package db

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// The genres of the films and shows a profile may see, across every library it may see, for
// Search before anything is typed. Each is found, and given its cover, by looking at no more
// than genreCandidates of its titles in index order, so the read costs the same whatever the
// size of the library. Counting is what grows with the library, so it is asked for apart.
const (
	maxGenres       = 200
	genreCandidates = 50
)

// Genre is one genre the viewer may see: its public id and name, its cover (the backdrop of
// the first of its titles the viewer may see that has one: the same title on every read until
// the library changes), and with counts how many films and shows the viewer may see carry it.
type Genre struct {
	PublicID, Name string
	CoverHash      string
	CoverRole      ImageRole // the kind of picture the cover is: a backdrop, or a library's own covers
	// Posters are the posters (for what is listened to or read, the covers) of the first two
	// of its titles the viewer may see that have one, two titles, the same two on every read
	// until the library changes: a phone's genre tile draws them.
	Posters    []string
	PosterRole ImageRole
	Count      int
}

// genrePosters is how many posters a genre carries.
const genrePosters = 2

// postersOf is the SQL for the file hashes, joined by commas, of the posters of the first
// genrePosters titles the viewer may see among a genre's candidates (an SQL query of item_id),
// in roles (an SQL list of image roles). visible is the viewer's condition on the items row i.
func postersOf(candidates, roles, visible string) string {
	return fmt.Sprintf(`(SELECT group_concat(h, ',') FROM (SELECT min(im.file_hash) AS h FROM (%s) c CROSS JOIN items i ON i.id = c.item_id
		CROSS JOIN images im ON im.item_id = i.id AND im.role IN %s AND im.selected = 1 WHERE im.file_hash IS NOT NULL AND %s
		GROUP BY i.id ORDER BY i.id LIMIT %d))`, candidates, roles, visible, genrePosters)
}

// splitPosters reads postersOf's answer.
func splitPosters(joined string) []string {
	if joined == "" {
		return []string{}
	}
	return strings.Split(joined, ",")
}

// Genres lists the genres of the films and shows the viewer may see, by name, at most 200.
// With library (a public id) it lists that one library's genres instead: see libraryGenres.
func (d *DB) Genres(ctx context.Context, v Viewer, library string, counts bool) ([]Genre, error) {
	if library != "" {
		return d.libraryGenres(ctx, v, library)
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
	// Every library the viewer may see, whatever its kind: the titles are picked by their own
	// kind below, so a library of any kind that holds films or shows (anime does) is taken in,
	// and one that holds none costs one empty look in the index.
	libraries, err := ids(ctx, tx, `SELECT l.id FROM libraries l WHERE `+acc.libraryVisible("l.id"))
	if err != nil || len(libraries) == 0 {
		return nil, err
	}
	in, kinds := intList(libraries), fmt.Sprintf("(%d, %d)", KindMovie, KindShow)
	// The first candidates of a genre, in the order of item_tags_library: by library, kind,
	// rating and item. With a rating limit the index skips what is above it.
	candidates := fmt.Sprintf(`SELECT x.item_id FROM item_tags x INDEXED BY item_tags_library WHERE x.library_id IN %s AND x.kind IN %s
		AND x.tag_id = t.id%s ORDER BY x.library_id, x.kind, x.rating_age, x.item_id LIMIT %d`, in, kinds, acc.rated("x", ""), genreCandidates)
	genres, err := queryAll(ctx, tx, fmt.Sprintf(`SELECT t.id, t.public_id, t.name, ifnull((SELECT im.file_hash FROM (%s) c
			CROSS JOIN items i ON i.id = c.item_id CROSS JOIN images im ON im.item_id = i.id AND im.role = %d AND im.selected = 1
			WHERE im.file_hash IS NOT NULL AND %s LIMIT 1), ''), ifnull(%s, '') FROM tags t WHERE t.kind = '%s'
		AND EXISTS (SELECT 1 FROM (%s) c CROSS JOIN items i ON i.id = c.item_id WHERE %s) ORDER BY t.norm_name LIMIT %d`,
		candidates, ImageBackdrop, acc.visible("i"), postersOf(candidates, fmt.Sprintf("(%d)", ImagePoster), acc.visible("i")),
		TagGenre, candidates, acc.visible("i"), maxGenres), v.args(),
		func(g *struct {
			id      int64
			posters string
			Genre
		}) []any {
			g.CoverRole, g.PosterRole = ImageBackdrop, ImagePoster
			return []any{&g.id, &g.PublicID, &g.Name, &g.CoverHash, &g.posters}
		})
	if err != nil || len(genres) == 0 {
		return nil, err
	}
	out := make([]Genre, len(genres))
	place := map[int64]int{}
	list := make([]int64, len(genres))
	for n, g := range genres {
		out[n], place[g.id], list[n] = g.Genre, n, g.id
		out[n].Posters = splitPosters(g.posters)
	}
	if !counts {
		return out, nil
	}
	// Counted from the index: the films and shows within the rating limit, less those a
	// blocked label hides and the shows left with nothing under them the viewer may see.
	n := fmt.Sprintf(`SELECT t.id, (SELECT count(*) FROM item_tags it WHERE it.tag_id = t.id AND it.library_id IN %s AND it.kind IN %s%s)
		FROM tags t WHERE t.id IN %s`, in, kinds, acc.rated("it", ""), intList(list))
	// What is taken away is read from item_tags alone, by its key: the row carries the item's
	// library, kind and rating. Each "+" keeps SQLite from reading every film's and show's tags
	// through another index instead.
	less := func(items []int64) string {
		return fmt.Sprintf(` UNION ALL SELECT it.tag_id, -count(*) FROM item_tags it WHERE it.item_id IN %s AND +it.tag_id IN %s
			AND +it.library_id IN %s AND +it.kind IN %s%s GROUP BY 1`, intList(items), intList(list), in, kinds, acc.rated("it", "+"))
	}
	// Both sets are only what the profile is kept from, so they are small. Each is read once and
	// named by id: inside the count, SQLite would work them out again.
	var blocked []int64
	if len(acc.blockedTags) > 0 {
		if blocked, err = ids(ctx, tx, acc.blockedIDs(0, kinds), v.args()...); err != nil {
			return nil, err
		}
		if len(blocked) > 0 {
			n += less(blocked)
		}
	}
	// Each such show once: one a blocked label covers was taken away just above.
	if set := acc.emptiedSet(0, fmt.Sprintf("(%d)", KindShow)); set != "" {
		emptied, err := ids(ctx, tx, set, v.args()...)
		if err != nil {
			return nil, err
		}
		emptied = slices.DeleteFunc(emptied, func(id int64) bool { return slices.Contains(blocked, id) })
		if len(emptied) > 0 {
			n += less(emptied)
		}
	}
	type count struct {
		tag int64
		n   int
	}
	sums, err := queryAll(ctx, tx, `WITH c(tag, n) AS MATERIALIZED (`+n+`) SELECT tag, sum(n) FROM c GROUP BY tag`, v.args(),
		func(c *count) []any { return []any{&c.tag, &c.n} })
	for _, c := range sums {
		out[place[c.tag]].Count = c.n
	}
	return out, err
}

// genreKinds are the titles a library's own genres are counted over, by the kind of library:
// a music library's genres are its albums'.
var genreKinds = map[string][]ItemKind{LibraryMovies: {KindMovie}, LibraryShows: {KindShow}, LibraryAnime: {KindMovie, KindShow},
	LibraryMusic: {KindAlbum}, LibraryAudiobooks: {KindAudiobook}, LibraryBooks: {KindBook, KindComic}}

// libraryGenres lists the genres of one library the viewer may see, by name, at most 200: a
// music library's Genres tab. Each has its count, the number of the library's albums (films,
// shows, books) the viewer may see that the genre opens to. It is the genre filter's own
// count (see Facets), so the tab and the filter never disagree, and an album counts for a
// genre its artist carries, as the list does. The cover is the picture of one such title that
// carries the genre itself: a film's or show's backdrop, else the title's own cover.
func (d *DB) libraryGenres(ctx context.Context, v Viewer, library string) ([]Genre, error) {
	var kind string
	var libraryID int64
	err := d.read.QueryRowContext(ctx, `SELECT id, kind FROM libraries WHERE public_id = ?`, library).Scan(&libraryID, &kind)
	if err != nil {
		return nil, notFound(err)
	}
	// Facets answers "not found" itself for a library the viewer may not see.
	counted, err := d.Facets(ctx, v, ItemQuery{Library: library, Kinds: genreKinds[kind]}, FacetGenre)
	if err != nil || len(counted) == 0 {
		return nil, err
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
	count := map[string]int{}
	args := v.args()
	marks := make([]string, len(counted))
	for n, c := range counted {
		count[c.Value], marks[n] = c.Count, fmt.Sprintf("@genre%d", n)
		args = append(args, sql.Named(fmt.Sprintf("genre%d", n), c.Value))
	}
	roles, posterRoles, posterRole := fmt.Sprintf("(%d)", ImageBackdrop), fmt.Sprintf("(%d)", ImagePoster), ImagePoster
	if kind != LibraryMovies && kind != LibraryShows && kind != LibraryAnime {
		roles = fmt.Sprintf("(%d, %d)", ImageCover, ImagePoster) // what is listened to or read has a cover
		posterRoles, posterRole = roles, ImageCover
	}
	// The first few titles of the library that carry the genre, in the order of the tag
	// table's own key, and the first of them the viewer may see that has a picture.
	cover := fmt.Sprintf(`FROM (SELECT x.item_id FROM item_tags x WHERE x.tag_id = t.id AND x.library_id = %d
			LIMIT %d) c CROSS JOIN items i ON i.id = c.item_id CROSS JOIN images im ON im.item_id = i.id AND im.role IN %s AND im.selected = 1
		WHERE im.file_hash IS NOT NULL AND %s LIMIT 1`, libraryID, genreCandidates, roles, acc.visible("i"))
	var kinds []int64
	for _, k := range genreKinds[kind] {
		kinds = append(kinds, int64(k))
	}
	// The posters are of the titles the genre is counted over (a music library's albums), and
	// as it counts them: a title carrying the genre itself, or one whose artist (its root) does.
	ofKinds := ""
	if len(kinds) > 0 {
		ofKinds = " AND x.kind IN " + intList(kinds)
	}
	candidates := fmt.Sprintf(`SELECT x.item_id FROM item_tags x WHERE x.tag_id = t.id AND x.library_id = %d%s LIMIT %d`,
		libraryID, ofKinds, genreCandidates)
	if len(kinds) > 0 {
		candidates = fmt.Sprintf(`SELECT item_id FROM (%s) UNION SELECT item_id FROM (SELECT a.id AS item_id FROM item_tags x
			CROSS JOIN items a ON a.root_id = x.item_id AND a.kind IN %s WHERE x.tag_id = t.id AND x.library_id = %d LIMIT %d)`,
			candidates, intList(kinds), libraryID, genreCandidates)
	}
	type row struct {
		Genre
		posters string
	}
	genres, err := queryAll(ctx, tx, fmt.Sprintf(`SELECT t.public_id, t.name, ifnull((SELECT im.file_hash %s), ''), ifnull((SELECT im.role %s), 0),
		ifnull(%s, '') FROM tags t WHERE t.public_id IN (%s) ORDER BY t.norm_name`, cover, cover, postersOf(candidates, posterRoles, acc.visible("i")),
		strings.Join(marks, ", ")), args,
		func(g *row) []any { return []any{&g.PublicID, &g.Name, &g.CoverHash, &g.CoverRole, &g.posters} })
	out := make([]Genre, len(genres))
	for n, g := range genres {
		out[n] = g.Genre
		out[n].Count, out[n].Posters, out[n].PosterRole = count[g.PublicID], splitPosters(g.posters), posterRole
	}
	return out, err
}
