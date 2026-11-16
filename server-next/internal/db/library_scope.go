package db

import (
	"context"
	"database/sql"
	"strings"
)

// librariesInScope resolves one library or an any: list. A hidden id is refused exactly as
// an absent one; callers never return a partial scope's counts or titles.
func librariesInScope(ctx context.Context, tx *sql.Tx, acc access, scope string) ([]int64, error) {
	names := []string{scope}
	if list, any := strings.CutPrefix(scope, "any:"); any {
		names = strings.Split(list, ",")
	}
	if len(names) > MaxConditionValues {
		return nil, invalid("library: name at most %d libraries", MaxConditionValues)
	}
	var out []int64
	seen := map[string]bool{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, invalid("library: name each library in the scope")
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		var id int64
		err := tx.QueryRowContext(ctx, `SELECT l.id FROM libraries l WHERE l.public_id = ? AND `+acc.libraryVisible("l.id"), name).Scan(&id)
		if err != nil {
			return nil, notFound(err)
		}
		out = append(out, id)
	}
	return out, nil
}
