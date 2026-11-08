package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points are the read-side twin of the merged scan issuer's source
// facts. They add no registration API: each shape is a closed point with its
// own native function/key/EQP audit.
const iaSourcePointOctetsSQL = `SELECT octet_length(id),octet_length(library_id),octet_length(kind),octet_length(name),octet_length(configured_root),octet_length(root),octet_length(classification),octet_length(incarnation),octet_length(root_identity),octet_length(health) FROM library_sources WHERE id=? AND library_id=?`
const iaSourceRootsOctetsSQL = `SELECT octet_length(library_id),octet_length(root) FROM catalog_libraries WHERE id=?`

const (
	iaSourcePointIndex       = "sqlite_autoindex_library_sources_1"
	iaSourceLibraryRootIndex = "sqlite_autoindex_library_sources_2"
)

func init() {
	type shape struct {
		name, sql, table, seek string
		params                 []string
		eqp                    string
	}
	for _, v := range []shape{
		{"point", iaSourcePointOctetsSQL, "library_sources", "id=?", []string{"source", "library"}, "SEARCH library_sources USING INDEX " + iaSourcePointIndex + " (id=?)"},
		{"roots", iaSourceRootsOctetsSQL, "catalog_libraries", "rowid=?", []string{"library"}, "SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)"},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.source." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA source octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(v.params) || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != v.seek || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i] || p.Nullable {
					return false
				}
			}
			if v.name == "point" {
				if len(d.Params) != 2 || d.Params[0].Type != stmt.Text || d.Params[1].Type != stmt.Text || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != iaSourcePointIndex || len(d.Plan.PrimaryKeys) != 0 {
					return false
				}
			} else if len(d.Params) != 1 || d.Params[0].Type != stmt.Integer || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "catalog_libraries" || len(d.Plan.Indexes) != 0 {
				return false
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error {
			return auditIASourceOctets(ctx, c, v.table, v.name)
		}, func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != v.eqp {
				return fmt.Errorf("IA source octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}

// auditIASourceOctets proves the unshadowed native octet_length and the exact
// native key. library_sources.id is a TEXT primary key backed by the pinned pk
// autoindex; catalog_libraries.id is the INTEGER PRIMARY KEY rowid.
func auditIASourceOctets(ctx context.Context, c *sql.Conn, table, shape string) error {
	functions, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IA source: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA source: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA source: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA source: native octet_length missing")
	}
	if shape == "point" {
		indexes, e := query(ctx, c, "PRAGMA main.index_list("+table+")")
		if e != nil {
			return e
		}
		found, unique := false, false
		for _, row := range indexes.Values {
			if len(row) != 5 {
				return fmt.Errorf("IA source: invalid native index metadata")
			}
			if row[1] == iaSourcePointIndex {
				if _, err := autoindexOrigin(row); err != nil {
					return fmt.Errorf("IA source: %w", err)
				}
				found = true
			}
			// The point filters this pair; keep its UNIQUE(library_id,root)
			// constraint certified so one library cannot own two equal roots.
			if row[1] == iaSourceLibraryRootIndex {
				if origin, err := autoindexOrigin(row); err != nil || origin != "u" {
					return fmt.Errorf("IA source: native library/root UNIQUE required")
				}
				unique = true
			}
		}
		if !found {
			return fmt.Errorf("IA source: pinned native id key missing")
		}
		if !unique {
			return fmt.Errorf("IA source: native library/root UNIQUE missing")
		}
		info, e := query(ctx, c, "PRAGMA main.index_info("+iaSourcePointIndex+")")
		if e != nil {
			return e
		}
		if len(info.Values) != 1 || len(info.Values[0]) != 3 || info.Values[0][2] != "id" {
			return fmt.Errorf("IA source: native key must be exactly id")
		}
		info, e = query(ctx, c, "PRAGMA main.index_info("+iaSourceLibraryRootIndex+")")
		if e != nil {
			return e
		}
		if len(info.Values) != 2 || info.Values[0][2] != "library_id" || info.Values[1][2] != "root" {
			return fmt.Errorf("IA source: native library/root key must be exactly library_id,root")
		}
		return nil
	}
	columns, e := query(ctx, c, "PRAGMA main.table_xinfo("+table+")")
	if e != nil {
		return e
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("IA source: invalid native columns")
		}
		n, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("IA source: invalid native key metadata")
		}
		if n > 0 {
			if n != 1 || row[1] != "id" || row[2] != "INTEGER" || row[6] != int64(0) {
				return fmt.Errorf("IA source: exact native primary key required")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("IA source: native primary key missing")
	}
	return nil
}
