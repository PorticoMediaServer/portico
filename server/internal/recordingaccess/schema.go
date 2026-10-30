package recordingaccess

import (
	"context"
	"database/sql"
	"sync/atomic"
)

// The P10 schema probe asks `sqlite_master` whether the direct identity tables
// are installed. That is a fact about the database file, settled before the
// listener serves its first request and unchanged for the life of the process —
// yet it was being asked on every library-authority check, which is once per
// request plus once per item on a listing. Over a 2,204-row `sqlite_master` that
// is 0.08 ms of nothing, several hundred times a second.
//
// Schema resolves it once per database handle. It is deliberately per-handle
// rather than package-level: two Services over two databases in one process
// (which is what the test suite is) must not teach each other what their schema
// looks like.
type Schema struct {
	// tables is 0 while unknown and count+1 once resolved, so the zero value of
	// the struct is "not yet asked" without a separate flag.
	tables atomic.Int32
}

// NewSchema returns an unresolved probe cache.
func NewSchema() *Schema { return &Schema{} }

func (s *Schema) directTables(ctx context.Context, tx *sql.Tx) (int, error) {
	if s != nil {
		if cached := s.tables.Load(); cached > 0 {
			return int(cached - 1), nil
		}
	}
	var tables int
	if err := tx.QueryRowContext(ctx, directSchemaQuery).Scan(&tables); err != nil {
		return 0, err
	}
	if s != nil {
		s.tables.Store(int32(tables) + 1)
	}
	return tables, nil
}
