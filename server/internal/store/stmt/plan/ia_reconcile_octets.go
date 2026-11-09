package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points are the read-side byte preflight for IA reconcile source
// preparation. They add no registration API: each shape is a closed point with
// its own native function/key/EQP audit.
const (
	iaReconcileLibraryOctetsSQL = `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=?`
	iaReconcileSourceOctetsSQL  = `SELECT octet_length(id) FROM library_sources WHERE id=? AND library_id=?`
	iaReconcileObjectOctetsSQL  = `SELECT octet_length(id),octet_length(source_id),octet_length(relative_path),octet_length(object_identity),octet_length(revision),octet_length(directory_path),octet_length(state) FROM inventory_objects WHERE id=? AND source_id=? AND state='available' AND retired=0`
)

func init() {
	type shape struct {
		name, sql, table, eqp, mode string
		index                       string
		keys                        []string
		params                      []stmt.Param
	}
	for _, v := range []shape{
		{"library", iaReconcileLibraryOctetsSQL, "catalog_libraries", "SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)", "rowid", "", []string{"id"}, []stmt.Param{{Name: "library", Type: stmt.Integer}}},
		{"source", iaReconcileSourceOctetsSQL, "library_sources", "SEARCH library_sources USING INDEX sqlite_autoindex_library_sources_1 (id=?)", "index", iaReconcileSourceIndex, []string{"id"}, []stmt.Param{{Name: "source", Type: stmt.Text}, {Name: "library", Type: stmt.Text}}},
		{"object", iaReconcileObjectOctetsSQL, "inventory_objects", "SEARCH inventory_objects USING INDEX sqlite_autoindex_inventory_objects_1 (id=?)", "index", iaReconcileObjectIndex, []string{"id"}, []stmt.Param{{Name: "object", Type: stmt.Text}, {Name: "source", Type: stmt.Text}}},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.reconcile." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA reconcile octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != iaReconcileSeek(v.mode) || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Params) != len(v.params) {
				return false
			}
			if v.mode == "index" {
				if len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != v.index || len(d.Plan.PrimaryKeys) != 0 {
					return false
				}
			} else if len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != v.table || len(d.Plan.Indexes) != 0 {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i].Name || p.Type != v.params[i].Type || p.Nullable {
					return false
				}
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error {
			if v.mode == "index" {
				return auditClosedOctetKey(ctx, c, v.table, v.index, v.keys)
			}
			return auditIARowidOctetKey(ctx, c, v.table, v.keys[0])
		}, func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != v.eqp {
				return fmt.Errorf("IA reconcile octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}

const (
	iaReconcileSourceIndex = "sqlite_autoindex_library_sources_1"
	iaReconcileObjectIndex = "sqlite_autoindex_inventory_objects_1"
)

func iaReconcileSeek(mode string) string {
	if mode == "index" {
		return "id=?"
	}
	return "rowid=?"
}

// auditIARowidOctetKey proves the unshadowed native octet_length and one exact
// INTEGER PRIMARY KEY column (an implicit rowid alias) for an IA read point.
func auditIARowidOctetKey(ctx context.Context, c *sql.Conn, table, key string) error {
	functions, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IA octet point: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA octet point: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA octet point: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA octet point: native octet_length missing")
	}
	columns, e := query(ctx, c, "PRAGMA main.table_xinfo("+identifier(table)+")")
	if e != nil {
		return e
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("IA octet point: invalid native columns")
		}
		position, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("IA octet point: invalid native key metadata")
		}
		if position <= 0 {
			continue
		}
		if position != 1 || row[1] != key || row[2] != "INTEGER" || row[6] != int64(0) {
			return fmt.Errorf("IA octet point: exact native primary key required")
		}
		keys++
	}
	if keys != 1 {
		return fmt.Errorf("IA octet point: native primary key missing")
	}
	return nil
}
