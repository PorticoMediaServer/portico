package dbwork

import (
	"context"
	"database/sql"
	"regexp"
	"slices"
	"strings"
	"sync"
)

type changeKey struct{}
type changeSet struct {
	mu sync.Mutex
	// tables keys are every general root the transaction named, exactly as
	// before. The value says whether any named statement was eligible to wake an
	// exact subscriber: true for a real or unproven write, false only for a
	// plain read (statementHead.plainSelect). A false-valued key is still a root,
	// so general subscribers, dependency expansion and unknown broadcasts see
	// precisely what they saw before.
	tables  map[string]bool
	unknown bool
}

func (s *changeSet) note(statement string) {
	if s == nil {
		return
	}
	// A CTE or multi-statement command may write several roots. Preserve safety
	// by broadcasting whenever the bounded head parser cannot prove its scope.
	head := headOf(statement)
	unknown, table, ok := head.unknown, head.table, head.named
	if !ok && !unknown {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unknown = s.unknown || unknown || table == ""
	if table != "" {
		if s.tables == nil {
			s.tables = map[string]bool{}
		}
		// Reads insert the key but never clear it: a real write to the same root,
		// before or after the read, keeps the exact wake.
		s.tables[table] = s.tables[table] || !head.plainSelect
	}
}

// known is the exact-wake set: every table a statement in the set named, except
// roots only a plain read named. A named root whose statement's scope couldn't
// be proven is still eligible — for example `WITH x AS (SELECT 1) UPDATE
// accounts …` wakes the accounts subscriber today and still does. The values are
// conservative: an unreadable read shape counts as a write, so this is not a
// complete SQL write parser.
func (s *changeSet) known() map[string]bool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]bool{}
	for table, exact := range s.tables {
		if exact {
			out[table] = true
		}
	}
	return out
}

// roots is the general set: every key, whatever its value, so a read still
// expands trigger and foreign-key dependencies and unknown shapes still
// broadcast exactly as before this change.
func (s *changeSet) roots() map[string]bool {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unknown || len(s.tables) == 0 {
		return nil
	}
	out := map[string]bool{}
	for table := range s.tables {
		out[table] = true
	}
	return out
}

var wakeDependencies = struct {
	sync.RWMutex
	edges map[string]map[string]bool
	// expanded caches each root set's closure. A scan commits the same few
	// root sets thousands of times, and with this schema's triggers a closure
	// is hundreds of tables: walking it on every commit was a third of the
	// server's CPU during a scan (OPS-1 M2 profile). Cleared whenever an edge
	// is added; cached sets are shared and read-only.
	expanded map[string]map[string]bool
}{edges: map[string]map[string]bool{}, expanded: map[string]map[string]bool{}}

// expandedCacheLimit bounds the closure cache; past it the cache starts over.
const expandedCacheLimit = 4096

var mutationTarget = regexp.MustCompile(`(?i)\b(?:INSERT\s+(?:OR\s+\w+\s+)?INTO|REPLACE\s+INTO|UPDATE\s+(?:OR\s+\w+\s+)?|DELETE\s+FROM)\s*["\x60\[]?(\w+)`)
var referenceTarget = regexp.MustCompile(`(?i)\bREFERENCES\s+["\x60\[]?(\w+)`)

// InstallWakeDependencies follows trigger writes and foreign-key edges so a
// worker subscribed to a derived job table hears the original catalog commit.
// This is one schema read on open, never a read on commit or a request path.
func InstallWakeDependencies(ctx context.Context, db *sql.DB) error {
	rows, e := db.QueryContext(ctx, `SELECT type,name,tbl_name,COALESCE(sql,'') FROM sqlite_schema WHERE type IN('table','trigger') AND name NOT LIKE 'sqlite_%'`)
	if e != nil {
		return e
	}
	defer rows.Close()
	type object struct{ kind, name, table, sql string }
	var objects []object
	tables := map[string]bool{}
	for rows.Next() {
		var o object
		if e = rows.Scan(&o.kind, &o.name, &o.table, &o.sql); e != nil {
			return e
		}
		objects = append(objects, o)
		if o.kind == "table" {
			tables[strings.ToLower(o.name)] = true
		}
	}
	if e = rows.Err(); e != nil {
		return e
	}
	edges := map[string]map[string]bool{}
	add := func(from, to string) {
		from, to = strings.ToLower(from), strings.ToLower(to)
		if !tables[from] || !tables[to] {
			return
		}
		if edges[from] == nil {
			edges[from] = map[string]bool{}
		}
		edges[from][to] = true
	}
	for _, o := range objects {
		if o.kind == "trigger" {
			for _, m := range mutationTarget.FindAllStringSubmatch(o.sql, -1) {
				add(o.table, m[1])
			}
		}
		if o.kind == "table" {
			for _, m := range referenceTarget.FindAllStringSubmatch(o.sql, -1) {
				add(m[1], o.name)
			}
		}
	}
	wakeDependencies.Lock()
	defer wakeDependencies.Unlock()
	clear(wakeDependencies.expanded)
	// Handles in one process can be test databases with different schemas. Union
	// edges is conservative: it may wake extra workers but never hides a cascade.
	for from, targets := range edges {
		if wakeDependencies.edges[from] == nil {
			wakeDependencies.edges[from] = map[string]bool{}
		}
		for to := range targets {
			wakeDependencies.edges[from][to] = true
		}
	}
	return nil
}

// expandedChanges is roots plus every table a trigger or foreign key can
// change from them. The result is shared: callers must not modify it.
func expandedChanges(roots map[string]bool) map[string]bool {
	if roots == nil {
		return nil
	}
	names := make([]string, 0, len(roots))
	for table := range roots {
		names = append(names, table)
	}
	slices.Sort(names)
	key := strings.Join(names, "\x00")
	wakeDependencies.RLock()
	cached, ok := wakeDependencies.expanded[key]
	wakeDependencies.RUnlock()
	if ok {
		return cached
	}
	wakeDependencies.Lock()
	defer wakeDependencies.Unlock()
	if cached, ok = wakeDependencies.expanded[key]; ok {
		return cached
	}
	queue := names
	for len(queue) > 0 {
		table := queue[0]
		queue = queue[1:]
		for target := range wakeDependencies.edges[table] {
			if !roots[target] {
				roots[target] = true
				queue = append(queue, target)
			}
		}
	}
	if len(wakeDependencies.expanded) >= expandedCacheLimit {
		clear(wakeDependencies.expanded)
	}
	wakeDependencies.expanded[key] = roots
	return roots
}
