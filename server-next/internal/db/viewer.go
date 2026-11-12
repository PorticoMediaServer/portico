package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrInvalid is a request the data layer refuses as malformed. Its message is safe to show.
var ErrInvalid = errors.New("invalid")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// Viewer is who is reading: an account and the viewing profile it chose.
type Viewer struct {
	UserID    int64
	ProfileID int64
}

// args is the named value every statement that reads the viewer's own state must be given.
func (v Viewer) args() []any { return []any{sql.Named("viewer_profile", v.ProfileID)} }

// access is what one viewer may see, read once at the start of a request in the same
// transaction as the request's own statements.
type access struct {
	known        bool    // the profile exists and belongs to the account
	allLibraries bool    // no library limit on the account or the profile
	libraries    []int64 // otherwise, the libraries both allow
	maxAge       *int    // the lower of the account's rating limit and the profile's
	allowUnrated bool    // with a limit: whether a title with no rating is shown
	blockedTags  []int64 // labels the account or the profile must never be shown
}

func ids(ctx context.Context, tx *sql.Tx, query string, args ...any) ([]int64, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func loadAccess(ctx context.Context, tx *sql.Tx, v Viewer) (access, error) {
	var a access
	var userAll, profileAll bool
	var accountAge *int
	var accountUnrated bool
	err := tx.QueryRowContext(ctx, `SELECT u.all_libraries, p.all_libraries, p.max_rating_age, p.allow_unrated, u.max_rating_age, u.allow_unrated
		FROM profiles p JOIN users u ON u.id = p.user_id WHERE p.id = ? AND u.id = ?`, v.ProfileID, v.UserID).
		Scan(&userAll, &profileAll, &a.maxAge, &a.allowUnrated, &accountAge, &accountUnrated)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.known, a.allLibraries = true, userAll && profileAll
	// The limit on the whole account and the profile's own both apply: the lower age wins,
	// and an unrated title is shown only if every limit that is set allows it.
	switch {
	case accountAge == nil:
	case a.maxAge == nil:
		a.maxAge, a.allowUnrated = accountAge, accountUnrated
	default:
		lower := min(*a.maxAge, *accountAge)
		a.maxAge, a.allowUnrated = &lower, a.allowUnrated && accountUnrated
	}
	if !a.allLibraries {
		// The account must be allowed the library, and so must the profile.
		if a.libraries, err = ids(ctx, tx, `SELECT id FROM libraries WHERE
			(? OR id IN (SELECT library_id FROM library_access WHERE user_id = ?))
			AND (? OR id IN (SELECT library_id FROM library_access WHERE profile_id = ?))`,
			userAll, v.UserID, profileAll, v.ProfileID); err != nil {
			return a, err
		}
	}
	a.blockedTags, err = ids(ctx, tx, `SELECT tag_id FROM profile_restrictions WHERE profile_id = ?
		UNION SELECT tag_id FROM account_restrictions WHERE user_id = ?`, v.ProfileID, v.UserID)
	return a, err
}

func intList(values []int64) string {
	parts := make([]string, len(values))
	for n, value := range values {
		parts[n] = strconv.FormatInt(value, 10)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

// libraryVisible is the condition that the viewer may see the library whose id is the SQL
// expression library.
func (a access) libraryVisible(library string) string {
	switch {
	case !a.known:
		return "0"
	case a.allLibraries:
		return "1"
	}
	// The "+" keeps this a check on rows found some other way. Without it SQLite may pick an
	// index because the library list fits it, and give up the index that orders the list.
	return "+" + library + " IN " + intList(a.libraries)
}

// visible is the one condition that says the viewer may see the items row with this alias.
// Every read of items puts it inside its own statement (lists, totals, detail, people, their
// counts, and the state writes), so a restricted title is never fetched and then filtered.
//
// A row is visible when its library is, its rating is within the profile's limit, and neither
// it nor anything it sits under (its album or season, its artist or show) carries a label the
// profile has blocked. Children are stored with the rating they inherit, so an episode needs
// no join to its show. When a limit is set, a title with no rating is hidden: unknown is not
// the same as suitable.
//
// A profile with no restriction gets the condition "1", so its reads cost nothing extra.
//
// A show, season, artist, album, collection or book series whose every playable title is
// hidden from the profile is hidden too, so a restricted profile never meets an empty holder.
// See filled.
func (a access) visible(alias string) string {
	return "(" + a.libraryVisible(alias+".library_id") + a.rated(alias, "+") + a.unblocked(alias) + a.filled(alias) + ")"
}

// filled is the holder part of visible, as " AND ..." or nothing: the row cannot be left
// empty (see holder_flags.go), holds nothing, or holds at least one playable title the
// profile may see. A holder's titles are in its library, so only a rating limit or a blocked
// label can hide all of them, and a profile with neither gets nothing here. A row that is not
// flagged costs one look at a column of the row in hand; a flagged one stops at the first
// title the profile may see. It suits one row or a page; a list or a count over many rows
// uses emptiedSet instead.
func (a access) filled(alias string) string {
	shown := a.titleUnder(alias)
	if shown == "" {
		return ""
	}
	return " AND (" + alias + ".may_empty = 0 OR " + holdsNothing(alias) + " OR " + shown + ")"
}

// holdsNothing is the condition that the holder row with this alias has no titles under it
// as children. leaf_count counts those; a collection or a book series holds its titles by
// relation and has none, so for those kinds it says nothing and titleUnder decides.
func holdsNothing(alias string) string {
	return "(" + alias + ".leaf_count = 0 AND " + alias + ".kind NOT IN " + relationHolderKinds + ")"
}

// titleUnder is the condition that the holder row with this alias holds a playable title the
// profile may see, or "" for a profile that is kept from nothing.
func (a access) titleUnder(alias string) string {
	leaf := alias + "_leaf" // its own name, as visible may itself be checking a row named c
	check := a.rated(leaf, "") + a.unblocked(leaf)
	if check == "" {
		return ""
	}
	// Titles nest at most three deep, so a holder is the root or the parent of its titles;
	// each is found through its own index (see underHolder). A collection holds its films by
	// relation instead, read through the relations' primary key, and a series holds books by
	// relation and comic issues as children. Each kind looks only where it can hold titles.
	under := func(column string) string {
		return "EXISTS (SELECT 1 FROM items " + leaf + " WHERE " + leaf + "." + column + " = " + alias + ".id AND " +
			leaf + ".kind IN " + playableKindsSQL + check + ")"
	}
	related := "EXISTS (SELECT 1 FROM item_relations " + leaf + "_r JOIN items " + leaf + " ON " + leaf + ".id = " + leaf + "_r.related_item_id WHERE " +
		leaf + "_r.item_id = " + alias + ".id AND " + leaf + "_r.kind IN " + heldByRelation + " AND " + leaf + ".kind IN " + playableKindsSQL + check + ")"
	return "(CASE " + alias + ".kind WHEN " + itoa(int64(KindCollection)) + " THEN " + related +
		" WHEN " + itoa(int64(KindSeries)) + " THEN (" + related + " OR " + under("parent_id") + ")" +
		" ELSE (" + under("root_id") + " OR " + under("parent_id") + ") END)"
}

// emptiedSet is a query for the holders the profile would see but for being left with nothing
// under them: the flagged holders of a library and some kinds (0 and "" mean any) that are
// within its limits themselves and hold no playable title it may see. It reads only the flagged holders, through their own small index. A list
// reads it once for the request and leaves those ids out of every statement it runs, so a
// row costs nothing more however many rows a count or a jump reads. "" for a profile that is
// kept from nothing.
func (a access) emptiedSet(library int64, kinds string) string {
	return a.emptiedWithin(library, kinds, "")
}

// emptiedWithin is emptiedSet for a list that is of one title, person or collection: within
// is the condition on the holder row f that it is one of the list's rows (query.within), read
// through that condition's own index instead of every flagged holder of the server. "" is
// emptiedSet.
func (a access) emptiedWithin(library int64, kinds, within string) string {
	shown := a.titleUnder("f")
	if shown == "" {
		return ""
	}
	from := `items f INDEXED BY items_may_empty WHERE f.may_empty = 1`
	if within != "" {
		from = `items f WHERE ` + within + ` AND f.may_empty = 1`
	}
	in := ""
	if library != 0 {
		in += fmt.Sprintf(" AND f.library_id = %d", library)
	}
	if kinds != "" {
		in += " AND f.kind IN " + kinds
	}
	// A holder the profile may not see anyway needs no look under it.
	return `SELECT f.id FROM ` + from + in + a.rated("f", "") + a.unblocked("f") +
		` AND NOT ` + holdsNothing("f") + ` AND NOT ` + shown
}

// rated is the rating part of visible, as " AND ..." or nothing. plus is "+" to keep it a
// check on rows found some other way (see libraryVisible), or "" where the rating index
// should be used: a total, or a list read from that index.
func (a access) rated(alias, plus string) string {
	switch {
	case a.maxAge == nil:
		return ""
	case a.allowUnrated:
		return " AND ifnull(" + alias + ".rating_age, 0) <= " + strconv.Itoa(*a.maxAge)
	}
	return " AND " + plus + alias + ".rating_age <= " + strconv.Itoa(*a.maxAge)
}

// unblocked is the label part of visible, as " AND ..." or nothing: none of the labels that
// cover the row is one the profile has blocked. The row carries them itself (see
// covering_labels.go), so this looks nothing up.
func (a access) unblocked(alias string) string {
	if len(a.blockedTags) == 0 {
		return ""
	}
	return " AND " + labelsExclude(alias+".labels", a.blockedTags)
}

// blockedIDs is a query for the id of every item a blocked label hides. A total subtracts
// these instead of checking every row. library and kinds narrow it to one library and some
// kinds; 0 and "" mean any. It reads the index of the items that have labels at all.
func (a access) blockedIDs(library int64, kinds string) string {
	return a.blockedWithin(library, kinds, "")
}

// blockedWithin is blockedIDs for a list that is of one title, person, collection or series:
// within is the condition on the row h that it is one of the list's rows (query.within), so
// only those rows are looked at instead of every labelled item of the server. "" is blockedIDs.
func (a access) blockedWithin(library int64, kinds, within string) string {
	in := ""
	if within != "" {
		in = " AND " + within
	}
	if library != 0 {
		in += fmt.Sprintf(" AND h.library_id = %d", library)
	}
	if kinds != "" {
		in += " AND h.kind IN " + kinds
	}
	return `SELECT h.id FROM items h WHERE h.labels IS NOT NULL AND NOT ` + labelsExclude("h.labels", a.blockedTags) + in
}

// sparseRated is how few visible titles make a rating-limited list "sparse". See ratedIn.
const sparseRated = 2000

// ratedIn counts, up to sparseRated, the items of some kinds in a library that a profile with
// a rating limit may see. A library of music or home video is mostly unrated, and reading
// such a list in order to find the few rated titles would read all of it. So a caller reads a
// sparse list from the rating index instead, and answers at once when the count is zero.
// It reports dense (sparseRated) when the profile has no limit to apply this way.
func (a access) ratedIn(ctx context.Context, tx *sql.Tx, library int64, kinds string) (int, error) {
	if a.maxAge == nil || a.allowUnrated {
		return sparseRated, nil
	}
	var n int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM items WHERE library_id = ? AND kind IN `+kinds+`
		AND rating_age <= ? LIMIT ?)`, library, *a.maxAge, sparseRated).Scan(&n)
	return n, err
}

// query builds one statement with named values, so the visibility condition can appear in it
// any number of times.
type query struct {
	sql  strings.Builder
	args []any
	// An item list filtered by a tag can read differently for its page and its total: from
	// says whether items is read through an index or by the ids the tag gathered, and tagPage
	// and tagCount stand in for the "{tag}" mark in the condition. See ItemQuery.filters.
	from, countFrom   string
	tagPage, tagCount string
	tagTotal          string // a whole statement for the total, when the tag allows a cheaper one
	// pagePlus is "+" when the page should not be found through a filter's own index; it
	// stands in for "{+}" in the page's condition, and nothing does in the total's.
	pagePlus string
	// labels is the viewer's blocked-label condition, standing in for "{labels}" on a page.
	// A total leaves it out and subtracts what the labels hide. See access.blockedIDs.
	labels string
	// ratingPlus stands in for "{rated}" on a page: "+" unless the page is read from the
	// rating index. See access.rated.
	ratingPlus string
	// plainLibrary and plainKinds are set when the list is a whole library and some kinds
	// with nothing else asked for and no rating limit. Its totals and tag counts can then be
	// read from item_tags and the labels alone.
	plainLibrary int64
	plainKinds   string
	// library and kinds are the list's own, when it names them, whatever else it asks for;
	// tagWalk is its tag condition as a check on one row. A total uses them to count what a
	// blocked label hides: only that library's hidden items are looked at, one by one.
	library int64
	kinds   string
	tagWalk string
	// holderKinds is the list's kinds that hold others (shows, seasons, artists, albums), as
	// an SQL list, or "" when it names none.
	holderKinds string
	// emptied is a query for the holders of this list the viewer is left with nothing under
	// (see access.emptiedSet), or "" when there are none. holders leaves them out, standing
	// in for "{holders}" on a page and in a count that checks labels row by row; a total
	// counts without it and subtracts.
	emptied, holders string
	// scope is a query for the ids of the rows this list can hold, when the list is of one
	// title, person, collection or series, or "": what the list leaves out (the holders it is
	// left with nothing under, what a blocked label hides) is then looked for among those rows
	// alone, not across the server. See within.
	scope string
	// whole is set when the list is all of that library and those kinds with nothing else
	// asked for, whatever the viewer's rating limit. Its tag choices are counted from indexes.
	whole bool
}

// within is the condition that the items row with this alias is one the list can hold, or ""
// when the list has no narrower scope than its library.
func (q *query) within(alias string) string {
	if q.scope == "" {
		return ""
	}
	return alias + ".id IN (" + q.scope + ")"
}

func (q *query) pageWhere() string {
	return strings.NewReplacer("{tag}", q.tagPage, "{+}", q.pagePlus, "{labels}", q.labels, "{holders}", q.holders,
		"{rated}", q.ratingPlus).Replace(q.sql.String())
}

// countWhere is the condition for a total or a facet. With labels false the blocked-label
// check is left out, for a caller that subtracts instead.
func (q *query) countWhere(labels bool) string {
	check, holders := "", ""
	if labels {
		check, holders = q.labels, q.holders
	}
	return strings.NewReplacer("{tag}", q.tagCount, "{+}", "", "{labels}", check, "{holders}", holders, "{rated}", "").Replace(q.sql.String())
}

// hiddenWhere is the list's condition for the few rows a blocked label hides: no label check,
// and the tag as a check on each row.
func (q *query) hiddenWhere() string {
	return strings.NewReplacer("{tag}", q.tagWalk, "{+}", "", "{labels}", "", "{holders}", "", "{rated}", "").Replace(q.sql.String())
}

func newQuery(v Viewer) *query {
	return &query{args: v.args(), from: "items i", countFrom: "items i", ratingPlus: "+"}
}

// arg adds a value and returns the name to write in the statement.
func (q *query) arg(value any) string {
	name := fmt.Sprintf("a%d", len(q.args))
	q.args = append(q.args, sql.Named(name, value))
	return "@" + name
}

func (q *query) add(parts ...string) {
	for _, p := range parts {
		q.sql.WriteString(p)
	}
}
