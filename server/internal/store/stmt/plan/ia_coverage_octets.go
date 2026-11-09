package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points are the read-side byte preflight for IA coverage
// preparation. They add no registration API: each shape is a closed point with
// its own native function/key/EQP audit.
const (
	iaCoverageLibraryOctetsSQL   = `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=?`
	iaCoverageSourceOctetsSQL    = `SELECT octet_length(id) FROM library_sources WHERE id=? AND library_id=?`
	iaCoverageDirectoryOctetsSQL = `SELECT octet_length(relative_path),octet_length(identity),octet_length(revision),octet_length(cursor),octet_length(state) FROM inventory_directories WHERE job_id=? AND relative_path=?`
)

func init() {
	type shape struct {
		name, sql, table, eqp, mode, seek string
		index                             string
		keys                              []string
		params                            []stmt.Param
	}
	for _, v := range []shape{
		{"library", iaCoverageLibraryOctetsSQL, "catalog_libraries", "SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)", "rowid", "rowid=?", "", []string{"id"}, []stmt.Param{{Name: "library", Type: stmt.Integer}}},
		{"source", iaCoverageSourceOctetsSQL, "library_sources", "SEARCH library_sources USING INDEX sqlite_autoindex_library_sources_1 (id=?)", "index", "id=?", "sqlite_autoindex_library_sources_1", []string{"id"}, []stmt.Param{{Name: "source", Type: stmt.Text}, {Name: "library", Type: stmt.Text}}},
		{"directory", iaCoverageDirectoryOctetsSQL, "inventory_directories", "SEARCH inventory_directories USING INDEX sqlite_autoindex_inventory_directories_1 (job_id=? AND relative_path=?)", "index", "job_id=? AND relative_path=?", "sqlite_autoindex_inventory_directories_1", []string{"job_id", "relative_path"}, []stmt.Param{{Name: "run", Type: stmt.Text}, {Name: "directory", Type: stmt.Text}}},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.coverage." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA coverage octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != v.seek || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Params) != len(v.params) {
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
				return fmt.Errorf("IA coverage octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}
