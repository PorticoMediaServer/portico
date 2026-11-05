package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
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

func auditInsert(ctx context.Context, conn *sql.Conn, d stmt.Definition, shape *dmlShape, bindings []any, vm Rows) (*InsertAudit, error) {
	table := nameKey(shape.table)
	var objectType string
	var root int64
	if err := conn.QueryRowContext(ctx, "SELECT type,rootpage FROM sqlite_schema WHERE name=? COLLATE NOCASE", table).Scan(&objectType, &root); err != nil {
		return nil, err
	}
	if objectType != "table" || root <= 0 {
		return nil, fmt.Errorf("destination must be an ordinary table")
	}
	if !mainWriteRoot(vm, root) {
		return nil, fmt.Errorf("native destination does not match audited main table")
	}
	a := &InsertAudit{Table: table, IndexColumns: map[string]Rows{}}
	var err error
	a.Columns, err = query(ctx, conn, "PRAGMA table_xinfo("+identifier(table)+")")
	if err != nil {
		return nil, err
	}
	a.Indexes, err = query(ctx, conn, "PRAGMA index_list("+identifier(table)+")")
	if err != nil {
		return nil, err
	}
	for _, index := range a.Indexes.Values {
		name := index[1].(string)
		info, err := query(ctx, conn, "PRAGMA index_xinfo("+identifier(name)+")")
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
	var key []string
	if len(d.Plan.Indexes) == 1 {
		wanted := d.Plan.Indexes[0]
		// Match the existing forced-index contract's plain identifier spelling;
		// never interpolate an unchecked metadata string into the probe SQL.
		tokens, err := lintTokens(wanted)
		if err != nil || len(tokens) != 1 || tokens[0].kind != 'w' {
			return nil, fmt.Errorf("conflict index must be a plain identifier")
		}
		p := lintParser{tokens: tokens}
		if err := p.name(); err != nil {
			return nil, err
		}
		for _, index := range a.Indexes.Values {
			if index[1] == wanted && index[2] == int64(1) && index[4] == int64(0) {
				for _, col := range a.IndexColumns[wanted].Values {
					if col[5] == int64(1) {
						name, ok := col[2].(string)
						if !ok {
							return nil, fmt.Errorf("conflict expression indexes are outside the grammar")
						}
						key = append(key, foldName(name))
					}
				}
			}
		}
	} else {
		if nameKey(d.Plan.PrimaryKeys[0]) != table {
			return nil, fmt.Errorf("conflict PK names the wrong table")
		}
		for order := int64(1); ; order++ {
			found := false
			for _, col := range a.Columns.Values {
				if col[5] == order {
					key = append(key, foldName(col[1].(string)))
					found = true
				}
			}
			if !found {
				break
			}
		}
	}
	conflict := make([]string, len(shape.conflict))
	for i, col := range shape.conflict {
		conflict[i] = nameKey(col)
	}
	if len(key) == 0 || !slices.Equal(key, conflict) {
		return nil, fmt.Errorf("conflict columns do not match expected full unique index/PK")
	}
	conflictRoot := root
	if len(d.Plan.Indexes) == 1 {
		if err := conn.QueryRowContext(ctx, "SELECT rootpage FROM sqlite_schema WHERE type='index' AND name=?", d.Plan.Indexes[0]).Scan(&conflictRoot); err != nil {
			return nil, err
		}
	}
	if !nativeConflictRoot(vm, conflictRoot) {
		return nil, fmt.Errorf("expected conflict key is not checked by native INSERT VM")
	}
	probe := &ConflictProbe{SQL: "SELECT 1 FROM " + shape.table, Bindings: []any{}}
	if len(d.Plan.Indexes) == 1 {
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
		predicates = append(predicates, col+"=?")
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
