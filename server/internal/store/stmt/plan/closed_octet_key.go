package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

// Callers pass only their compiled ID/SQL-qualified point's native key. This
// shared metadata check admits no function syntax, SQL text or receipt itself.
func auditClosedOctetKey(ctx context.Context, conn *sql.Conn, table, indexName string, keyColumns []string) error {
	// Native statement form: SQL objects named pragma_function_list cannot
	// counterfeit the builtin/override inventory.
	functions, err := query(ctx, conn, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("closed octet point: invalid native function metadata")
		}
		name, nameOK := row[0].(string)
		arity, arityOK := row[4].(int64)
		if !nameOK || !arityOK {
			return fmt.Errorf("closed octet point: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("closed octet point: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("closed octet point: unshadowed native octet_length required")
	}
	tables, err := query(ctx, conn, "PRAGMA main.table_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	if len(tables.Values) != 1 || len(tables.Values[0]) != 6 ||
		tables.Values[0][0] != "main" || tables.Values[0][1] != table || tables.Values[0][2] != "table" {
		return fmt.Errorf("closed octet point: ordinary MAIN table required")
	}
	columns, err := query(ctx, conn, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if err != nil {
		return err
	}
	// These are complete PRIMARY KEY points, not prefixes that merely SEARCH.
	keys := make([]string, len(keyColumns))
	count := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("closed octet point: invalid native table metadata")
		}
		position, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("closed octet point: invalid native key position")
		}
		if position != 0 {
			if position < 1 || position > int64(len(keys)) || row[6] != int64(0) {
				return fmt.Errorf("closed octet point: complete native key mismatch")
			}
			name, ok := row[1].(string)
			if !ok || keys[position-1] != "" {
				return fmt.Errorf("closed octet point: invalid native key column")
			}
			keys[position-1] = name
			count++
		}
	}
	if count != len(keyColumns) || !slices.Equal(keys, keyColumns) {
		return fmt.Errorf("closed octet point: complete native key mismatch")
	}
	if indexName == "" {
		if tables.Values[0][4] != int64(1) {
			return fmt.Errorf("closed octet point: WITHOUT ROWID primary key required")
		}
		return nil
	}
	indexes, err := query(ctx, conn, "PRAGMA main.index_list("+identifier(table)+")")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("closed octet point: invalid native index metadata")
		}
		if row[1] == indexName {
			origin, err := autoindexOrigin(row)
			if err != nil || origin != "pk" {
				return fmt.Errorf("closed octet point: pinned native primary key required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("closed octet point: pinned native key absent (possible renumbering)")
	}
	index, err := query(ctx, conn, "PRAGMA main.index_info("+identifier(indexName)+")")
	if err != nil {
		return err
	}
	if len(index.Values) != len(keyColumns) {
		return fmt.Errorf("closed octet point: complete native index key mismatch")
	}
	for i, row := range index.Values {
		if len(row) != 3 || row[0] != int64(i) || row[2] != keyColumns[i] {
			return fmt.Errorf("closed octet point: complete native index key mismatch")
		}
	}
	return nil
}
