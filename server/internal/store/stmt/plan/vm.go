package plan

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
)

type rootKey struct{ database, page int64 }

// Read each database's own sqlite_schema: page numbers are local to a database.
// Root 1 is sqlite_schema itself, which has no row in its own catalogue.
func readRoots(ctx context.Context, conn *sql.Conn) (map[rootKey]string, error) {
	dbs, err := query(ctx, conn, "PRAGMA database_list")
	if err != nil {
		return nil, err
	}
	roots := map[rootKey]string{}
	for _, db := range dbs.Values {
		number, name := db[0].(int64), db[1].(string)
		roots[rootKey{number, 1}] = identifier(name) + "." + identifier("sqlite_schema")
		objects, err := query(ctx, conn, "SELECT rootpage,name FROM "+identifier(name)+".sqlite_schema WHERE rootpage>0 ORDER BY rootpage,name")
		if err != nil {
			return nil, err
		}
		for _, object := range objects.Values {
			key := rootKey{number, object[0].(int64)}
			if _, exists := roots[key]; exists {
				return nil, fmt.Errorf("plan: ambiguous root page %d in %s", key.page, name)
			}
			roots[key] = identifier(name) + "." + identifier(object[1].(string))
		}
	}
	return roots, nil
}

// canonicalVM removes only physical identifiers from SQLite 3.53.4 EXPLAIN:
// Transaction P3 (schema cookie, when checked) and literal b-tree root operands.
// Operand meanings were checked against the pinned engine's VDBE source.
// Registers, including OpenWrite P2 with OPFLAG_P2ISREG, remain exact. Unknown
// roots fail closed; IntegrityCk's P4 root array is unsupported, never erased.
func canonicalVM(vm Rows, roots map[rootKey]string) (Rows, error) {
	if !slices.Equal(vm.Columns, []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}) {
		return Rows{}, fmt.Errorf("plan: unexpected EXPLAIN columns")
	}
	result := Rows{Columns: slices.Clone(vm.Columns), Values: make([][]any, len(vm.Values))}
	for i, original := range vm.Values {
		if len(original) != 8 {
			return Rows{}, fmt.Errorf("plan: unexpected EXPLAIN row %d", i)
		}
		for _, value := range original {
			if text, ok := value.(string); ok && strings.HasPrefix(text, "vtab:") {
				return Rows{}, fmt.Errorf("plan: vtab operand requires a fixed-slot native audit at row %d", i)
			}
		}
		row := slices.Clone(original)
		result.Values[i] = row
		opcode, ok := row[1].(string)
		if !ok {
			return Rows{}, fmt.Errorf("plan: invalid EXPLAIN opcode at row %d", i)
		}
		root, database := -1, -1
		switch opcode {
		case "Transaction", "OpenWrite":
			flags, ok := row[6].(int64)
			if !ok {
				return Rows{}, fmt.Errorf("plan: invalid %s P5 at row %d", opcode, i)
			}
			if opcode == "Transaction" {
				if flags != 0 {
					if _, ok := row[4].(int64); !ok {
						return Rows{}, fmt.Errorf("plan: invalid Transaction cookie at row %d", i)
					}
					row[4] = "<schema-cookie>"
				}
			} else if flags&0x10 == 0 { // OPFLAG_P2ISREG means P2 is a register.
				root, database = 3, 4
			}
		case "OpenRead", "ReopenIdx":
			root, database = 3, 4
		case "Clear":
			root, database = 2, 3
		case "Destroy":
			root, database = 2, 4
		case "TableLock":
			root, database = 3, 2
		case "IntegrityCk":
			return Rows{}, fmt.Errorf("plan: unsupported IntegrityCk root array at row %d", i)
		}
		if root >= 0 {
			page, pageOK := row[root].(int64)
			db, dbOK := row[database].(int64)
			name, found := roots[rootKey{db, page}]
			if !pageOK || !dbOK || !found {
				return Rows{}, fmt.Errorf("plan: unresolved %s root at row %d: database=%v page=%v", opcode, i, row[database], row[root])
			}
			row[root] = name
		}
	}
	return result, nil
}
