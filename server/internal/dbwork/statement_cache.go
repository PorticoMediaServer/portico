package dbwork

import (
	"container/list"
	"context"
	"database/sql/driver"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
)

// Statements cache SQLite bytecode, never rows or authorization decisions.
// Each physical connection owns its own bounded cache. Native SQLite storage is
// measured after bindings clear; SQL copies and Go bookkeeping have a separate
// conservative allowance. Drivers without this diagnostic use an estimate.
// The byte budget is the memory bound; the entry bound only stops a flood of
// tiny one-off statements. It must exceed the derived worker's working set
// (about 225 distinct statements, 2.7 MB): below it, LRU over that cycle
// misses almost every time and the writer re-prepares trigger-heavy
// statements for every title.
const statementCacheEntries = 512
const statementCacheBudget = 512 << 10
const statementCacheMaxBudget = 4 << 20
const statementCacheOverhead = 8 << 10
const statementCacheGoOverhead = 512

var statementBudget struct {
	sync.Once
	bytes int
}

func statementBudgetForMemory(memory int64) int {
	if memory <= 0 {
		return statementCacheBudget
	}
	return int(min(max(memory/512, int64(statementCacheBudget)), int64(statementCacheMaxBudget)))
}

func defaultStatementBudget() int {
	statementBudget.Do(func() { statementBudget.bytes = statementBudgetForMemory(hostlimits.EffectiveMemoryBytes()) })
	return statementBudget.bytes
}

// This optional driver diagnostic reads retained VM storage, not query values.
type statementMemory interface{ StatementMemoryBytes() int64 }

func retainedStatementSize(stmt driver.Stmt, query string) int {
	if measured, ok := stmt.(statementMemory); ok {
		if native := measured.StatementMemoryBytes(); native > 0 {
			// SQLite counts its SQL copy. The driver owns another C string and
			// the cache clones its key; 512 bytes covers their Go bookkeeping.
			overhead := int64(2*len(query) + statementCacheGoOverhead)
			if native > int64(^uint(0)>>1)-overhead {
				return int(^uint(0) >> 1)
			}
			return int(native + overhead)
		}
	}
	return statementCacheOverhead + 8*len(query)
}

// StatementCacheStats reports reuse without exposing SQL or bindings.
type StatementCacheStats struct {
	Hits      uint64 `json:"hits"`
	Misses    uint64 `json:"misses"`
	Bypasses  uint64 `json:"bypasses"`
	Evictions uint64 `json:"evictions"`
}

var statementReuse struct{ hits, misses, bypasses, evictions atomic.Uint64 }

func statementCacheStats() StatementCacheStats {
	return StatementCacheStats{Hits: statementReuse.hits.Load(), Misses: statementReuse.misses.Load(), Bypasses: statementReuse.bypasses.Load(), Evictions: statementReuse.evictions.Load()}
}
func resetStatementCacheStats() {
	statementReuse.hits.Store(0)
	statementReuse.misses.Store(0)
	statementReuse.bypasses.Store(0)
	statementReuse.evictions.Store(0)
}

type statementCache struct {
	mu      sync.Mutex
	entries map[string]*statementEntry
	lru     list.List
	bytes   int
	budget  int
	closed  bool
}
type statementEntry struct {
	query   string
	stmt    driver.Stmt
	size    int
	leased  bool
	retired bool
	element *list.Element
}

// statementShape recognizes SQL boundaries, not query semantics. Quotes and
// comments may contain semicolons; one trailing terminator is not a script.
// Trimming trailing comments/terminators also lets modernc retain prepare_v2
// bytecode instead of selecting its multi-statement execution path.
type statementShape struct {
	sql       string
	command   string
	single    bool
	temporary bool
	textBytes int
}

func describeStatement(query string) statementShape {
	shape := statementShape{single: true, textBytes: len(query)}
	start, last := -1, 0
	terminated := false
	for i := 0; i < len(query); {
		ch := query[i]
		if ch == ' ' || ch == '\t' || ch == '\r' || ch == '\n' || ch == '\f' {
			i++
			continue
		}
		if ch == '-' && i+1 < len(query) && query[i+1] == '-' {
			i += 2
			for i < len(query) && query[i] != '\n' && query[i] != '\r' {
				i++
			}
			continue
		}
		if ch == '/' && i+1 < len(query) && query[i+1] == '*' {
			i += 2
			for i+1 < len(query) && !(query[i] == '*' && query[i+1] == '/') {
				i++
			}
			if i+1 < len(query) {
				i += 2
			} else {
				i = len(query)
			}
			continue
		}
		if ch == ';' {
			terminated = true
			i++
			continue
		}
		if terminated {
			shape.single = false
			return shape
		}
		if start < 0 {
			start = i
		}
		tokenStart := i
		if ch == '\'' || ch == '"' || ch == '`' || ch == '[' {
			closing := ch
			if ch == '[' {
				closing = ']'
			}
			i++
			contentStart := i
			contentEnd := i
			closed := false
			for i < len(query) {
				if query[i] == closing {
					contentEnd = i
					i++
					if closing != ']' && i < len(query) && query[i] == closing {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				shape.single = false
				return shape
			}
			if ch != '\'' && strings.EqualFold(query[contentStart:contentEnd], "temp") {
				shape.temporary = true
			}
		} else if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || ch == '_' {
			i++
			for i < len(query) {
				c := query[i]
				if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_') {
					break
				}
				i++
			}
			word := query[tokenStart:i]
			if shape.command == "" {
				if len(word) <= 7 {
					shape.command = strings.ToUpper(word)
				} else {
					shape.command = "?"
				}
			}
			if strings.EqualFold(word, "temp") {
				shape.temporary = true
			}
		} else {
			i++
		}
		last = i
	}
	if start >= 0 {
		shape.sql = query[start:last]
	}
	return shape
}
func (s statementShape) ordinary() bool {
	switch s.command {
	case "SELECT", "WITH", "INSERT", "UPDATE", "DELETE", "REPLACE":
		return true
	}
	return false
}
func (s statementShape) retain() bool {
	return s.single && s.ordinary() && !s.temporary && s.textBytes <= (statementCacheBudget-statementCacheOverhead)/8
}
func (s statementShape) invalidates() bool {
	if !s.single || s.temporary {
		return true
	}
	if s.ordinary() || s.command == "EXPLAIN" {
		return false
	}
	// Unknown commands, DDL, ATTACH/DETACH and every PRAGMA remain conservative.
	return true
}

func invalidatingStatement(query string) bool { return describeStatement(query).invalidates() }

func (s *statementCache) remove(e *statementEntry) {
	delete(s.entries, e.query)
	s.lru.Remove(e.element)
	s.bytes -= e.size
	statementReuse.evictions.Add(1)
	e.retired = true
	if !e.leased {
		_ = e.stmt.Close()
	}
}

func (s *statementCache) invalidate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.entries {
		s.remove(e)
	}
}

func (s *statementCache) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, e := range s.entries {
		s.remove(e)
	}
}

// lease returns an exclusive handle and a release callback. Concurrent/nested
// readers of identical SQL use a transient handle, never an active cached VM.
// database/sql serializes physical connection calls; the mutex also protects
// release/invalidation bookkeeping when a canceled Rows is closed.
func (s *statementCache) lease(ctx context.Context, conn driver.Conn, query string) (driver.Stmt, func(error) error, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, nil, driver.ErrBadConn
	}
	if s.budget == 0 {
		s.budget = defaultStatementBudget()
	}
	if e := s.entries[query]; e != nil && !e.leased {
		e.leased = true
		s.lru.MoveToFront(e.element)
		statementReuse.hits.Add(1)
		return e.stmt, s.release(e), nil
	}
	shape := describeStatement(query)
	cacheable := shape.retain()
	if !cacheable {
		statementReuse.bypasses.Add(1)
		if shape.invalidates() {
			for _, e := range s.entries {
				s.remove(e)
			}
		}
	} else {
		statementReuse.misses.Add(1)
	}
	preparedSQL := query
	if shape.single && shape.sql != "" {
		preparedSQL = shape.sql
	}
	var stmt driver.Stmt
	var err error
	if p, ok := conn.(driver.ConnPrepareContext); ok {
		stmt, err = p.PrepareContext(ctx, preparedSQL)
	} else {
		stmt, err = conn.Prepare(preparedSQL)
	}
	if err != nil {
		return nil, nil, err
	}
	e := &statementEntry{query: strings.Clone(query), stmt: stmt, size: retainedStatementSize(stmt, query), leased: true, retired: true}
	// Do not replace an active entry of the same key or evict leased readers.
	if cacheable && e.size <= s.budget && s.entries[query] == nil {
		for s.lru.Len() >= statementCacheEntries || s.bytes+e.size > s.budget {
			var victim *statementEntry
			for p := s.lru.Back(); p != nil; p = p.Prev() {
				if candidate := p.Value.(*statementEntry); !candidate.leased {
					victim = candidate
					break
				}
			}
			if victim == nil {
				break
			}
			s.remove(victim)
		}
		if s.lru.Len() < statementCacheEntries && s.bytes+e.size <= s.budget {
			if s.entries == nil {
				s.entries = make(map[string]*statementEntry)
			}
			e.retired = false
			e.element = s.lru.PushFront(e)
			s.entries[e.query] = e
			s.bytes += e.size
		}
	}
	return stmt, s.release(e), nil
}

func (s *statementCache) release(e *statementEntry) func(error) error {
	return func(err error) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		if !e.leased {
			return nil
		}
		e.leased = false
		// Errors may leave driver bindings or VM state incomplete. Finalize instead
		// of allowing the next request to inherit any such state.
		if !e.retired && err != nil {
			s.remove(e)
			return nil
		}
		if e.retired {
			return e.stmt.Close()
		}
		// Reset/clear-binding has completed before this callback. Reprepare,
		// result buffers and function auxiliary storage can change footprint
		// without changing SQL. Charge current retained memory on every release.
		size := retainedStatementSize(e.stmt, e.query)
		if size > s.budget {
			s.remove(e)
			return nil
		}
		s.bytes += size - e.size
		e.size = size
		for s.bytes > s.budget {
			var victim *statementEntry
			for p := s.lru.Back(); p != nil; p = p.Prev() {
				if candidate := p.Value.(*statementEntry); !candidate.leased {
					victim = candidate
					break
				}
			}
			if victim == nil {
				break
			}
			s.remove(victim)
		}
		return nil
	}
}

func (c *observedConn) cachedExec(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	stmt, release, err := c.statements.lease(ctx, c.inner, query)
	if err != nil {
		return nil, err
	}
	if execer, ok := stmt.(driver.StmtExecContext); ok {
		result, err := execer.ExecContext(ctx, args)
		if closeErr := release(err); err == nil {
			err = closeErr
		}
		return result, err
	}
	release(driver.ErrSkip)
	return c.inner.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *observedConn) cachedQuery(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, func(error) error, error) {
	stmt, release, err := c.statements.lease(ctx, c.inner, query)
	if err != nil {
		return nil, nil, err
	}
	if queryer, ok := stmt.(driver.StmtQueryContext); ok {
		rows, err := queryer.QueryContext(ctx, args)
		if err != nil {
			release(err)
			return nil, nil, err
		}
		return rows, release, nil
	}
	release(driver.ErrSkip)
	rows, err := c.inner.(driver.QueryerContext).QueryContext(ctx, query, args)
	return rows, nil, err
}
