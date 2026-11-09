package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ErrNotFound is the one answer for a thing that does not exist and for a thing this viewer may
// not see, so a refusal never confirms that a hidden title exists.
var ErrNotFound = errors.New("not found")

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

// args are the two named values every statement that uses visibleTo must be given.
func (v Viewer) args() []any {
	return []any{sql.Named("viewer_user", v.UserID), sql.Named("viewer_profile", v.ProfileID)}
}

// libraryVisibleTo is the condition that the viewer may see the library whose id is the SQL
// expression library: the account must be allowed it, and so must the profile.
func libraryVisibleTo(library string) string {
	return strings.ReplaceAll(`(EXISTS (SELECT 1 FROM profiles WHERE id = @viewer_profile AND user_id = @viewer_user)
  AND ((SELECT all_libraries FROM users WHERE id = @viewer_user) = 1
    OR {lib} IN (SELECT library_id FROM library_access WHERE user_id = @viewer_user))
  AND ((SELECT all_libraries FROM profiles WHERE id = @viewer_profile) = 1
    OR {lib} IN (SELECT library_id FROM library_access WHERE profile_id = @viewer_profile)))`, "{lib}", library)
}

// visibleTo is the one condition that says the viewer may see the items row with this alias.
// Every read of items uses it inside its own statement (lists, detail, people, counts and the
// state writes), so a restricted title is never fetched and then filtered.
//
// A row is visible when its library is, its rating is within the profile's limit, and neither
// it nor the title it belongs to carries a label the profile has blocked. Children are stored
// with the rating they inherit, so an episode needs no join to its show. When a limit is set,
// a title with no rating is hidden: unknown is not the same as suitable.
func visibleTo(alias string) string {
	return "(" + libraryVisibleTo(alias+".library_id") + strings.ReplaceAll(`
  AND ((SELECT max_rating_age FROM profiles WHERE id = @viewer_profile) IS NULL
    OR {i}.rating_age <= (SELECT max_rating_age FROM profiles WHERE id = @viewer_profile))
  AND NOT EXISTS (SELECT 1 FROM profile_restrictions vr JOIN item_tags vt ON vt.tag_id = vr.tag_id
    WHERE vr.profile_id = @viewer_profile AND vt.item_id IN ({i}.id, {i}.root_id)))`, "{i}", alias)
}

// query builds one statement with named values, so the visibility condition can appear in it
// any number of times.
type query struct {
	sql  strings.Builder
	args []any
}

func newQuery(v Viewer) *query { return &query{args: v.args()} }

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
