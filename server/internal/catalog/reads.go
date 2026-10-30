package catalog

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
)

// Every read in this package goes through s.read(). That is what lets a
// composite handler wrap its whole body in one `dbwork.WithReadSnapshot` and
// have the fifty-odd queries underneath it join that snapshot instead of taking
// a pooled connection each.
//
// Two things follow from it, and both matter more than the connection count.
// One: the rows a page is built from all come from one WAL frame boundary, so
// the "did the catalogue move under me?" check that several composers ran twice
// is answered by construction. Two: the statements carry the request's context,
// so the lane's deadline reaches them and the per-request statement counter can
// attribute them.
//
// A read outside a request keeps working exactly as before: with no snapshot and
// no context in hand, read() is `s.db` with a background context, which is what
// every call site had.

// reader is the read surface shared by a pooled handle and a transaction.
type reader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// reads binds a context to whichever handle should serve it.
type reads struct {
	ctx context.Context
	to  reader
}

func (r reads) Query(query string, args ...any) (*sql.Rows, error) {
	return r.QueryContext(r.ctx, query, args...)
}

func (r reads) QueryRow(query string, args ...any) *sql.Row {
	return r.QueryRowContext(r.ctx, query, args...)
}

// QueryContext and QueryRowContext keep the explicit-context call sites working
// unchanged, while still resolving the snapshot.
func (r reads) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q := r.resolve(ctx)
	query, err := r.servingSQL(ctx, q, query)
	if err != nil {
		return nil, err
	}
	return q.QueryContext(ctx, query, args...)
}

func (r reads) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q := r.resolve(ctx)
	serving, err := r.servingSQL(ctx, q, query)
	if err != nil {
		// QueryRow cannot return the rewrite error directly. Return a row that
		// always fails Scan rather than letting a scalar caller accept a slot
		// number as its count or content.
		return q.QueryRowContext(ctx, `SELECT 1 WHERE 0`)
	}
	return q.QueryRowContext(ctx, serving, args...)
}

// Empty overlays are equivalent to their indexed base table, even while a
// different derivation rebuilds. Read all overlay presence in the same snapshot;
// any referenced table with old rows still selects the full retained views.
var readOverlaySQL = func() string {
	parts := []string{}
	for _, table := range compactcatalog.ServingTables {
		parts = append(parts, "'"+table+"',EXISTS(SELECT 1 FROM "+table+"_previous WHERE generation=(SELECT generation FROM catalog_generations WHERE singleton=1))")
	}
	return "SELECT json_object(" + strings.Join(parts, ",") + ")"
}()

type readGenerationKey struct{}
type readGeneration struct {
	snapshot  *sql.Tx
	once      sync.Once
	retaining bool
	overlays  map[string]int
	err       error
}

// A published generation has no old rows to merge. Resolve that fact inside
// the request's snapshot, once, so ordinary reads keep the base tables' index
// plans. During a rebuild the retained views still serve the previous content.
func (r reads) servingSQL(ctx context.Context, q reader, query string) (string, error) {
	state, _ := ctx.Value(readGenerationKey{}).(*readGeneration)
	retaining := true
	if state != nil && state.snapshot == dbwork.Snapshot(ctx) && !dbwork.InWriteTx(ctx) {
		for _, table := range compactcatalog.ServingTables {
			if strings.Contains(query, table) {
				state.once.Do(func() {
					state.err = q.QueryRowContext(ctx, `SELECT retaining FROM catalog_generations WHERE singleton=1`).Scan(&state.retaining)
					if state.err == nil && state.retaining {
						var raw string
						state.err = q.QueryRowContext(ctx, readOverlaySQL).Scan(&raw)
						if state.err == nil {
							state.err = json.Unmarshal([]byte(raw), &state.overlays)
						}
					}
				})
				if state.err != nil {
					return "", state.err
				}
				retaining = false
				if state.retaining {
					for _, candidate := range compactcatalog.ServingTables {
						if state.overlays[candidate] != 0 && strings.Contains(query, candidate) {
							retaining = true
							break
						}
					}
				}
				break
			}
		}
	}
	if retaining {
		query = compactcatalog.ServingSQL(query)
	}
	return compactcatalog.SearchServingSQL(ctx, q, query)
}

func (r reads) resolve(ctx context.Context) reader {
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return tx
	}
	return r.to
}

// read returns the reader for this Service's request context.
func (s *Service) read() reads {
	ctx := s.rctx
	if ctx == nil {
		ctx = context.Background()
	}
	if s.readTx != nil {
		return reads{ctx: ctx, to: s.readTx}
	}
	if tx := dbwork.Snapshot(ctx); tx != nil {
		return reads{ctx: ctx, to: tx}
	}
	return reads{ctx: ctx, to: dbwork.ReadHandle(ctx, s.db)}
}

// Context returns the request context this Service was bound to, or the
// background context when it was not bound to one.
func (s *Service) Context() context.Context {
	if s.rctx == nil {
		return context.Background()
	}
	return s.rctx
}

// WithContext binds a Service to one request. The returned value shares this
// Service's database handle and its caches — only the context differs — so a
// handler can hand its deadline and its read snapshot to the catalogue without
// the catalogue having to thread a context through two hundred call sites.
func (s *Service) WithContext(ctx context.Context) *Service {
	if s == nil || ctx == nil {
		return s
	}
	clone := *s
	if snapshot := dbwork.Snapshot(ctx); snapshot != nil && !dbwork.InWriteTx(ctx) {
		state, _ := ctx.Value(readGenerationKey{}).(*readGeneration)
		if state == nil || state.snapshot != snapshot {
			ctx = context.WithValue(ctx, readGenerationKey{}, &readGeneration{snapshot: snapshot})
		}
	}
	clone.rctx = ctx
	return &clone
}

// PrepareRead initializes the catalog's cursor signing key before a read
// snapshot opens. Rating classification runs in a background worker; a request
// never drains that queue.
func (s *Service) PrepareRead(ctx context.Context, restrictions interface{ Active() bool }) error {
	if s == nil {
		return nil
	}
	if _, err := s.cursorKey(); err != nil {
		return err
	}
	return nil
}

// WithRecommendationRestrictions scopes related-item projections alongside the
// request snapshot. It never mutates the process-wide Service.
func (s *Service) WithRecommendationRestrictions(r identity.ContentRestrictions) *Service {
	clone := *s
	clone.recRestrictions = r
	return &clone
}
