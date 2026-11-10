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
	maxAge       *int    // the profile's rating limit
	allowUnrated bool    // with a limit: whether a title with no rating is shown
	blockedTags  []int64 // labels the profile must never be shown
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
	err := tx.QueryRowContext(ctx, `SELECT u.all_libraries, p.all_libraries, p.max_rating_age, p.allow_unrated FROM profiles p
		JOIN users u ON u.id = p.user_id WHERE p.id = ? AND u.id = ?`, v.ProfileID, v.UserID).Scan(&userAll, &profileAll, &a.maxAge, &a.allowUnrated)
	if errors.Is(err, sql.ErrNoRows) {
		return a, nil
	}
	if err != nil {
		return a, err
	}
	a.known, a.allLibraries = true, userAll && profileAll
	if !a.allLibraries {
		// The account must be allowed the library, and so must the profile.
		if a.libraries, err = ids(ctx, tx, `SELECT id FROM libraries WHERE
			(? OR id IN (SELECT library_id FROM library_access WHERE user_id = ?))
			AND (? OR id IN (SELECT library_id FROM library_access WHERE profile_id = ?))`,
			userAll, v.UserID, profileAll, v.ProfileID); err != nil {
			return a, err
		}
	}
	a.blockedTags, err = ids(ctx, tx, `SELECT tag_id FROM profile_restrictions WHERE profile_id = ?`, v.ProfileID)
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
func (a access) visible(alias string) string {
	return "(" + a.libraryVisible(alias+".library_id") + a.rated(alias, "+") + a.unblocked(alias) + ")"
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

// unblocked is the label part of visible, as " AND ..." or nothing: neither the row nor what
// it sits under carries a blocked label. It is a few key lookups per row, which suits a page
// (the list is read in order and stops when the page is full) and a single item.
func (a access) unblocked(alias string) string {
	if len(a.blockedTags) == 0 {
		return ""
	}
	return " AND NOT EXISTS (SELECT 1 FROM item_tags blocked WHERE blocked.item_id IN (" + alias + ".id, " + alias + ".parent_id, " +
		alias + ".root_id) AND blocked.tag_id IN " + intList(a.blockedTags) + ")"
}

// blockedIDs is a query for the id of every item a blocked label hides: the items that carry
// one, and everything under them. A total subtracts these instead of checking every row.
// inLibrary and ofKinds narrow it to a library and kinds, as conditions on c, and may be
// empty. What is under a labelled title is in the title's library, so the library is checked
// on the labelled title (one row each), and what is under it is found with its kind from an
// index: that part reads no rows of the hidden items themselves.
func (a access) blockedIDs(inLibrary, ofKinds string) string {
	tags := intList(a.blockedTags)
	holder, holderIn := "", ""
	if inLibrary != "" {
		holder, holderIn = ` CROSS JOIN items r ON r.id = h.item_id`, strings.ReplaceAll(inLibrary, "c.", "r.")
	}
	return `SELECT c.id FROM item_tags h CROSS JOIN items c ON c.id = h.item_id WHERE h.tag_id IN ` + tags + inLibrary + ofKinds + `
		UNION SELECT c.id FROM item_tags h` + holder + ` CROSS JOIN items c ON c.parent_id = h.item_id WHERE h.tag_id IN ` + tags + holderIn + ofKinds + `
		UNION SELECT c.id FROM item_tags h` + holder + ` CROSS JOIN items c ON c.root_id = h.item_id WHERE h.tag_id IN ` + tags + holderIn + ofKinds
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
	// plainLibrary and plainKinds are set when the list is a whole library and kind with
	// nothing else asked for, as conditions on c, for counting what labels hide in it.
	plainLibrary, plainKinds string
}

func (q *query) pageWhere() string {
	return strings.NewReplacer("{tag}", q.tagPage, "{+}", q.pagePlus, "{labels}", q.labels, "{rated}", q.ratingPlus).Replace(q.sql.String())
}

// countWhere is the condition for a total or a facet. With labels false the blocked-label
// check is left out, for a caller that subtracts instead.
func (q *query) countWhere(labels bool) string {
	check := ""
	if labels {
		check = q.labels
	}
	return strings.NewReplacer("{tag}", q.tagCount, "{+}", "", "{labels}", check, "{rated}", "").Replace(q.sql.String())
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
