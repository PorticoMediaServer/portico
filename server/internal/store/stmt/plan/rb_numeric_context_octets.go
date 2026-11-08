package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rbNumericContextScalarOctetsSQL = "SELECT octet_length(e.title),octet_length(e.release_date),octet_length(e.added_at) FROM catalog_entities AS e WHERE e.id=?"

// Only this compiled complete-entity-PK byte point may preflight the scalar
// payload copied by the numeric-context window anchor. It grants no range,
// expression or column-selection permission.
func init() {
	expected := stmt.Definition{
		ID: stmt.ID("read.browse.rb.numeric_context.scalar_octets"), Owner: "store/read", Kind: stmt.Read,
		SQL:    rbNumericContextScalarOctetsSQL,
		Params: []stmt.Param{{Name: "entity", Type: stmt.Integer}},
		Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}},
	}
	pointOctets.add(octetShape{id: expected.ID, sql: expected.SQL, mismatch: "exact numeric-context scalar complete entity byte point required",
		definition: func(d stmt.Definition) bool {
			return d.ID == expected.ID && d.SQL == expected.SQL && d.Owner == expected.Owner && d.Kind == expected.Kind && reflect.DeepEqual(d.Params, expected.Params) && reflect.DeepEqual(d.Plan, expected.Plan)
		},
		audit: func(ctx context.Context, c *sql.Conn) error {
			return auditRBNumericContextEntityPK(ctx, c)
		},
		point: func(rows Rows) error {
			if len(rows.Values) != 1 || len(rows.Values[0]) != 4 {
				return fmt.Errorf("numeric-context byte point requires one complete-PK SEARCH")
			}
			text, ok := rows.Values[0][3].(string)
			if !ok || text != "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("numeric-context byte point requires its exact complete-PK SEARCH")
			}
			return nil
		},
	})
}

// auditRBNumericContextEntityPK proves the ordinary rowid table's INTEGER
// PRIMARY KEY is the complete point key: not a prefix, not an autoindex shape.
func auditRBNumericContextEntityPK(ctx context.Context, c *sql.Conn) error {
	tables, err := query(ctx, c, "PRAGMA main.table_list(catalog_entities)")
	if err != nil {
		return err
	}
	if len(tables.Values) != 1 || len(tables.Values[0]) != 6 || tables.Values[0][0] != "main" || tables.Values[0][1] != "catalog_entities" || tables.Values[0][2] != "table" || tables.Values[0][4] != int64(0) {
		return fmt.Errorf("numeric-context byte point requires an ordinary rowid entity table")
	}
	columns, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_entities)")
	if err != nil {
		return err
	}
	found := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("numeric-context byte point: invalid table metadata")
		}
		if row[5] == int64(0) {
			continue
		}
		if row[1] != "id" || row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
			return fmt.Errorf("numeric-context byte point requires the exact INTEGER entity primary key")
		}
		found++
	}
	if found != 1 {
		return fmt.Errorf("numeric-context byte point requires one complete entity primary key")
	}
	return nil
}
