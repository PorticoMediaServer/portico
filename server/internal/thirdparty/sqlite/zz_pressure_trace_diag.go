package sqlite

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strings"
	"sync"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// Branch-only diagnostic. No bound values are expanded; the fixture is synthetic.
type pressureDomainKey struct{}

func WithPressureDomain(ctx context.Context, domain string) context.Context {
	return context.WithValue(ctx, pressureDomainKey{}, domain)
}

type pressureNativeKey struct{ Domain, Level, Kind, Query string }

var pressureNativeCounts = struct {
	sync.Mutex
	counts map[pressureNativeKey]int64
}{counts: map[pressureNativeKey]int64{}}

type pressureNativeState struct{ domain string }

var pressureNativeStates sync.Map // sqlite3* -> state, owned by the executing connection

func pressureNativeTrace(tls *libc.TLS, _ uint32, token, pstmt uintptr, sql uintptr) int32 {
	value, ok := pressureNativeStates.Load(token)
	if !ok {
		return 0
	}
	query := strings.TrimSpace(libc.GoString(sql))
	level := "root"
	rootQuery := strings.TrimSpace(libc.GoString(sqlite3.Xsqlite3_sql(tls, pstmt)))
	if query == rootQuery {
		// A caller's leading SQL comment is still a root statement.
	} else if strings.HasPrefix(query, "-- TRIGGER ") {
		level = "trigger-entry"
		query = strings.TrimPrefix(query, "-- TRIGGER ")
	} else if strings.HasPrefix(query, "-- ") {
		level = "trigger-body"
		query = strings.TrimPrefix(query, "-- ")
	}
	kind := pressureSQLKind(query)
	query = strings.Join(strings.Fields(query), " ")
	key := pressureNativeKey{value.(*pressureNativeState).domain, level, kind, query}
	pressureNativeCounts.Lock()
	pressureNativeCounts.counts[key]++
	pressureNativeCounts.Unlock()
	return 0
}

func pressureSQLKind(query string) string {
	for {
		query = strings.TrimSpace(query)
		if strings.HasPrefix(query, "--") {
			if end := strings.IndexByte(query, '\n'); end >= 0 {
				query = query[end+1:]
				continue
			}
		} else if strings.HasPrefix(query, "/*") {
			if end := strings.Index(query, "*/"); end >= 0 {
				query = query[end+2:]
				continue
			}
		}
		if fields := strings.Fields(query); len(fields) != 0 {
			return strings.ToUpper(fields[0])
		}
		return "empty"
	}
}

type pressureNativeScope struct {
	conn     *conn
	state    *pressureNativeState
	previous string
}

func (c *conn) watchPressureTrace(ctx context.Context) pressureNativeScope {
	domain, _ := ctx.Value(pressureDomainKey{}).(string)
	if domain == "" {
		return pressureNativeScope{}
	}
	value, _ := pressureNativeStates.LoadOrStore(c.db, &pressureNativeState{})
	state := value.(*pressureNativeState)
	previous := state.domain
	state.domain = domain
	sqlite3.Xsqlite3_trace_v2(c.tls, c.db, sqlite3.SQLITE_TRACE_STMT, cFuncPointer(pressureNativeTrace), c.db)
	return pressureNativeScope{c, state, previous}
}

func (scope pressureNativeScope) restore() {
	if scope.conn == nil {
		return
	}
	scope.state.domain = scope.previous
	if scope.previous == "" {
		sqlite3.Xsqlite3_trace_v2(scope.conn.tls, scope.conn.db, 0, 0, 0)
		pressureNativeStates.Delete(scope.conn.db)
	}
}

func DumpPressureNativeTrace() {
	pressureNativeCounts.Lock()
	defer pressureNativeCounts.Unlock()
	keys := make([]pressureNativeKey, 0, len(pressureNativeCounts.counts))
	for key := range pressureNativeCounts.counts {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := keys[i], keys[j]
		return a.Domain+"\x00"+a.Level+"\x00"+a.Query < b.Domain+"\x00"+b.Level+"\x00"+b.Query
	})
	for _, key := range keys {
		entry, _ := json.Marshal(struct {
			pressureNativeKey
			Count int64
		}{key, pressureNativeCounts.counts[key]})
		log.Printf("PRESSURE-NATIVE %s", entry)
	}
}
