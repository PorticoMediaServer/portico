package plan

import (
	"context"
	"database/sql"
	"fmt"
)

type dmlTable struct {
	name         string
	root         int64
	columns      Rows
	withoutRowid bool
}

func inspectDMLTable(ctx context.Context, conn *sql.Conn, shape *dmlShape) (*dmlTable, error) {
	t := &dmlTable{}
	var kind string
	if err := conn.QueryRowContext(ctx, "SELECT name,type,rootpage FROM main.sqlite_schema WHERE name=? COLLATE NOCASE", nameKey(shape.table)).Scan(&t.name, &kind, &t.root); err != nil {
		return nil, err
	}
	if kind != "table" || t.root <= 0 {
		return nil, fmt.Errorf("DML destination must be an ordinary main table")
	}
	var err error
	t.columns, err = query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(t.name)+")")
	if err != nil {
		return nil, err
	}
	var wr int64
	if err := conn.QueryRowContext(ctx, "SELECT wr FROM pragma_table_list WHERE schema='main' AND name=?", t.name).Scan(&wr); err != nil {
		return nil, err
	}
	t.withoutRowid = wr != 0
	known := map[string]bool{}
	for _, col := range t.columns.Values {
		known[foldName(col[1].(string))] = true
	}
	if !t.withoutRowid {
		for _, alias := range []string{"rowid", "_rowid_", "oid"} {
			known[alias] = true
		}
	}
	for _, ref := range shape.references {
		parts := make([]string, len(ref.parts))
		for i, p := range ref.parts {
			parts[i] = nameKey(p)
		}
		if len(parts) == 0 {
			return nil, fmt.Errorf("empty DML column reference")
		}
		validQualifier := len(parts) == 1 || len(parts) == 2 && parts[0] == foldName(t.name) || len(parts) == 3 && parts[0] == "main" && parts[1] == foldName(t.name)
		if !validQualifier || !known[parts[len(parts)-1]] {
			return nil, fmt.Errorf("unknown %s column %v on %s", ref.role, ref.parts, t.name)
		}
	}
	return t, nil
}
