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
	allowUnrated bool    // under a rating limit, titles with no rating are shown too
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
	return library + " IN " + intList(a.libraries)
}

// visible is the one condition that says the viewer may see the items row with this alias.
// Every read of items puts it inside its own statement (lists, totals, detail, people, their
// counts, and the state writes), so a restricted title is never fetched and then filtered.
//
// A row is visible when its library is, its rating is within the profile's limit, and neither
// it nor anything it sits under (its album or season, its artist or show) carries a label the
// profile has blocked. Children are stored with the rating they inherit, so an episode needs
// no join to its show. When a limit is set, a title with no rating is hidden: unknown is not
// the same as suitable. The profile's "Allow unrated titles" switch shows them all the same.
//
// A profile with no restriction gets the condition "1", so its reads cost nothing extra.
func (a access) visible(alias string) string {
	cond := a.libraryVisible(alias + ".library_id")
	if a.maxAge != nil && a.allowUnrated {
		cond += " AND ifnull(" + alias + ".rating_age, 0) <= " + strconv.Itoa(*a.maxAge)
	} else if a.maxAge != nil {
		cond += " AND " + alias + ".rating_age <= " + strconv.Itoa(*a.maxAge)
	}
	if len(a.blockedTags) > 0 {
		blocked := "(SELECT item_id FROM item_tags WHERE tag_id IN " + intList(a.blockedTags) + ")"
		cond += " AND " + alias + ".id NOT IN " + blocked + " AND ifnull(" + alias + ".parent_id, 0) NOT IN " + blocked +
			" AND ifnull(" + alias + ".root_id, 0) NOT IN " + blocked
	}
	return "(" + cond + ")"
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
	// pagePlus is "+" when the page should not be found through a filter's own index; it
	// stands in for "{+}" in the page's condition, and nothing does in the total's.
	pagePlus string
}

func (q *query) pageWhere() string {
	return strings.NewReplacer("{tag}", q.tagPage, "{+}", q.pagePlus).Replace(q.sql.String())
}

func (q *query) countWhere() string {
	return strings.NewReplacer("{tag}", q.tagCount, "{+}", "").Replace(q.sql.String())
}

func newQuery(v Viewer) *query { return &query{args: v.args(), from: "items i", countFrom: "items i"} }

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
