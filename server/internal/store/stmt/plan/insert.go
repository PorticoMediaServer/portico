package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// INSERT has no destination SEARCH in native EQP. Retain its actual schema
// indexes and the complete FK audit instead. UPSERT additionally proves a
// unique/PK conflict key through a separate, explicitly identified point probe.
type InsertAudit struct {
	Table        string          `json:"table"`
	Columns      Rows            `json:"columns"`
	Indexes      Rows            `json:"indexes"`
	IndexColumns map[string]Rows `json:"indexColumns"`
	Conflict     *ConflictProbe  `json:"conflict,omitempty"`
}
type ConflictProbe struct {
	SQL       string `json:"sql"`
	Bindings  []any  `json:"bindings"`
	QueryPlan Rows   `json:"queryPlan"`
	Index     string `json:"index"`
}

func nameKey(name string) string {
	if len(name) >= 2 {
		switch name[0] {
		case '"', '`':
			quote := name[:1]
			name = strings.ReplaceAll(name[1:len(name)-1], quote+quote, quote)
		case '[':
			name = name[1 : len(name)-1]
		}
	}
	return foldName(name)
}
func foldName(name string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, name)
}

func auditInsert(ctx context.Context, conn *sql.Conn, d stmt.Definition, shape *dmlShape, table *dmlTable, bindings []any, vm Rows) (*InsertAudit, error) {
	a := &InsertAudit{Table: table.name, Columns: table.columns, IndexColumns: map[string]Rows{}}
	var err error
	a.Indexes, err = query(ctx, conn, "PRAGMA main.index_list("+identifier(table.name)+")")
	if err != nil {
		return nil, err
	}
	for _, index := range a.Indexes.Values {
		name := index[1].(string)
		info, err := query(ctx, conn, "PRAGMA main.index_xinfo("+identifier(name)+")")
		if err != nil {
			return nil, err
		}
		a.IndexColumns[name] = info
	}
	if len(shape.conflict) == 0 {
		if len(d.Plan.Indexes)+len(d.Plan.PrimaryKeys)+len(d.Plan.Seeks) != 0 {
			return nil, fmt.Errorf("plain INSERT has no read-seek contract; destination indexes are audited")
		}
		return a, nil
	}
	if len(d.Plan.Indexes)+len(d.Plan.PrimaryKeys) != 1 {
		return nil, fmt.Errorf("UPSERT requires exactly one expected conflict index or PK")
	}
	target, err := resolveConflict(a, table, shape)
	if err != nil {
		return nil, err
	}
	conflictRoot := table.root
	if len(d.Plan.Indexes) == 1 {
		wanted := d.Plan.Indexes[0]
		if target.nativePrimary || wanted != target.index {
			return nil, fmt.Errorf("expected index %s differs from resolved conflict target %s", wanted, target.index)
		}
		tokens, err := lintTokens(wanted)
		if err != nil || len(tokens) != 1 || tokens[0].kind != 'w' {
			return nil, fmt.Errorf("conflict index must be a plain identifier")
		}
		p := lintParser{tokens: tokens}
		if err := p.name(); err != nil {
			return nil, err
		}
		if err := conn.QueryRowContext(ctx, "SELECT rootpage FROM main.sqlite_schema WHERE type='index' AND name=?", target.index).Scan(&conflictRoot); err != nil {
			return nil, err
		}
	} else if !target.nativePrimary || foldName(d.Plan.PrimaryKeys[0]) != foldName(table.name) {
		return nil, fmt.Errorf("expected PK differs from resolved conflict target %s", target.index)
	}
	if !nativeConflictRoot(vm, conflictRoot) {
		return nil, fmt.Errorf("resolved conflict key is not checked by native INSERT VM")
	}

	probe := &ConflictProbe{SQL: "SELECT 1 FROM " + shape.table, Bindings: []any{}, Index: target.index}
	if len(d.Plan.Indexes) == 1 && !isAutoindex(d.Plan.Indexes[0]) {
		probe.SQL += " INDEXED BY " + d.Plan.Indexes[0]
	}
	var predicates []string
	for _, col := range shape.conflict {
		position := -1
		for i, inserted := range shape.columns {
			if nameKey(inserted) == nameKey(col) {
				position = i
				break
			}
		}
		if position < 0 {
			return nil, fmt.Errorf("conflict key must have an explicit bound INSERT value")
		}
		collation := ""
		for _, key := range target.columns {
			if key.name == nameKey(col) {
				collation = key.collation
				break
			}
		}
		if collation != "BINARY" && collation != "NOCASE" {
			return nil, fmt.Errorf("unsupported conflict collation %q", collation)
		}
		predicates = append(predicates, col+" COLLATE "+collation+"=?")
		probe.Bindings = append(probe.Bindings, bindings[position])
	}
	probe.SQL += " WHERE " + strings.Join(predicates, " AND ")
	probe.QueryPlan, err = query(ctx, conn, "EXPLAIN QUERY PLAN "+probe.SQL, probe.Bindings...)
	if err != nil {
		return nil, err
	}
	lookup := d
	lookup.SQL = probe.SQL
	if err := checkBase(lookup, probe.QueryPlan); err != nil {
		return nil, err
	}
	if err := checkSearch(lookup, probe.QueryPlan); err != nil {
		return nil, err
	}
	probe.QueryPlan, err = canonicalEQP(probe.QueryPlan)
	if err != nil {
		return nil, err
	}
	a.Conflict = probe
	return a, nil
}

// Prove the expected key is actually read by the INSERT's main VM, not merely
// available to the synthetic probe. Trigger subprogram cursors are separate.
func nativeConflictRoot(vm Rows, root int64) bool {
	cursors := map[int64]int64{}
	for i, row := range vm.Values {
		if len(row) != 8 {
			return false
		}
		if i > 0 && row[0] == int64(0) {
			break
		}
		cursor, ok := row[2].(int64)
		if !ok {
			return false
		}
		switch row[1] {
		case "OpenWrite", "OpenRead", "ReopenIdx":
			delete(cursors, cursor)
			page, pageOK := row[3].(int64)
			flags, flagsOK := row[6].(int64)
			if !pageOK || !flagsOK {
				return false
			}
			if row[4] == int64(0) && flags&0x10 == 0 {
				cursors[cursor] = page
			}
		case "Close":
			delete(cursors, cursor)
		case "NoConflict", "NotExists":
			if page, ok := cursors[cursor]; ok && page == root {
				return true
			}
		}
	}
	return false
}

func writesDatabase(vm Rows) bool {
	for _, row := range vm.Values {
		if len(row) == 8 && row[1] == "Transaction" {
			if mode, ok := row[3].(int64); ok && mode != 0 {
				return true
			}
		}
	}
	return false
}

func mainWriteRoot(vm Rows, root int64) bool {
	for i, row := range vm.Values {
		if len(row) != 8 {
			return false
		}
		if i > 0 && row[0] == int64(0) {
			break
		}
		if row[1] == "OpenWrite" && row[3] == root && row[4] == int64(0) {
			flags, ok := row[6].(int64)
			if ok && flags&0x10 == 0 {
				return true
			}
		}
	}
	return false
}
