package librarychannels

import (
	"context"
	"database/sql"
)

// querier is what the generator's helpers need: a transaction, or the
// transaction's statement cache.
type querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// stmtCache prepares each distinct statement once per transaction. Scheduling
// runs the same handful of statements for every slot of a batch (and the first
// day of a preview), so parsing and planning them once, rather than per slot,
// takes that cost off weak hosts. The statements belong to the transaction and
// close when it ends; a cache is never used after that.
type stmtCache struct {
	tx *sql.Tx
	m  map[string]*sql.Stmt
}

func newStmtCache(tx *sql.Tx) *stmtCache { return &stmtCache{tx: tx, m: map[string]*sql.Stmt{}} }

// stmtCacheLimit bounds the distinct statements one transaction keeps. The
// generator's statement texts are stable (a few dozen shapes), so this is only
// a backstop against a shape that varies per call.
const stmtCacheLimit = 64

func (c *stmtCache) prepared(ctx context.Context, query string) *sql.Stmt {
	if s, ok := c.m[query]; ok {
		return s
	}
	if len(c.m) >= stmtCacheLimit {
		return nil
	}
	s, e := c.tx.PrepareContext(ctx, query)
	if e != nil {
		// Run it unprepared instead, which reports the same error to the caller.
		return nil
	}
	c.m[query] = s
	return s
}

func (c *stmtCache) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if s := c.prepared(ctx, query); s != nil {
		return s.ExecContext(ctx, args...)
	}
	return c.tx.ExecContext(ctx, query, args...)
}

func (c *stmtCache) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if s := c.prepared(ctx, query); s != nil {
		return s.QueryContext(ctx, args...)
	}
	return c.tx.QueryContext(ctx, query, args...)
}

func (c *stmtCache) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if s := c.prepared(ctx, query); s != nil {
		return s.QueryRowContext(ctx, args...)
	}
	return c.tx.QueryRowContext(ctx, query, args...)
}

// statements returns the generation's statement cache for tx, starting a new
// one when the generation moves to another transaction.
func (g *generation) statements(tx *sql.Tx) *stmtCache {
	if g.stmts == nil || g.stmts.tx != tx {
		g.stmts = newStmtCache(tx)
	}
	return g.stmts
}
