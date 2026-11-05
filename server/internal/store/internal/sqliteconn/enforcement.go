package sqliteconn

import (
	"context"
	"database/sql"
	"fmt"
)

// Enforcement records settings that can disable constraints or weaken the
// snapshot contract. Resource and durability policy remain in Config/Assert.
type Enforcement struct {
	ForeignKeys            int
	IgnoreCheckConstraints int
	DeferForeignKeys       int
	WritableSchema         int
	ReadUncommitted        int
}

// ErrForeignKeysDisabled is a specific policy failure used by schema bootstrap
// and restart verification. It also matches ErrPolicy through errors.Is.
var ErrForeignKeysDisabled = fmt.Errorf("%w: foreign_keys must be ON", ErrPolicy)

// AssertEnforcement reads, never repairs, enforcement on an exclusively owned
// physical connection. Call outside a transaction; no deferred mode is admitted.
func AssertEnforcement(ctx context.Context, conn *sql.Conn) (Enforcement, error) {
	var e Enforcement
	if conn == nil {
		return e, ErrPolicy
	}
	for _, query := range []struct {
		sql  string
		dest *int
	}{
		{"PRAGMA foreign_keys", &e.ForeignKeys},
		{"PRAGMA ignore_check_constraints", &e.IgnoreCheckConstraints},
		{"PRAGMA defer_foreign_keys", &e.DeferForeignKeys},
		{"PRAGMA writable_schema", &e.WritableSchema},
		{"PRAGMA read_uncommitted", &e.ReadUncommitted},
	} {
		if err := conn.QueryRowContext(ctx, query.sql).Scan(query.dest); err != nil {
			return e, err
		}
	}
	if e.ForeignKeys != 1 {
		return e, fmt.Errorf("%w: got %d", ErrForeignKeysDisabled, e.ForeignKeys)
	}
	if e.IgnoreCheckConstraints != 0 || e.DeferForeignKeys != 0 || e.WritableSchema != 0 || e.ReadUncommitted != 0 {
		return e, fmt.Errorf("%w: enforcement %+v", ErrPolicy, e)
	}
	return e, nil
}
