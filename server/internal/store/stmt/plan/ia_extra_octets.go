package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points preflight the variable payload of one extra member and its
// episode membership. They add no registration API: each shape is a closed point
// with its own native function/key/EQP audit. The link octet point is already
// registered by the merged IA audio family and is reused, never re-registered.
const iaExtraItemOctetsSQL = `SELECT octet_length(library_id),octet_length(parent_path),octet_length(kind),octet_length(title),octet_length(asset_id),octet_length(relative_path) FROM item_extras WHERE item_id=?`
const iaExtraEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`
const iaExtraBoundaryOctetsSQL = `SELECT octet_length(asset_id),octet_length(status) FROM episode_asset_boundaries WHERE item_id=? AND asset_id=?`

type iaExtraKey struct{ name, kind string }

func init() {
	for _, v := range []struct {
		name, sql, table, eqp, pk, index string
		keys                             []iaExtraKey
		params                           []iaExtraKey
	}{
		{"entity", iaExtraEntityOctetsSQL, "catalog_entities", "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)", "catalog_entities", "", []iaExtraKey{{"id", "INTEGER"}}, []iaExtraKey{{"entity", "INTEGER"}, {"library", "INTEGER"}}},
		{"extra", iaExtraItemOctetsSQL, "item_extras", "SEARCH item_extras USING INTEGER PRIMARY KEY (rowid=?)", "item_extras", "", []iaExtraKey{{"item_id", "INTEGER"}}, []iaExtraKey{{"item", "INTEGER"}}},
		{"boundary", iaExtraBoundaryOctetsSQL, "episode_asset_boundaries", "SEARCH episode_asset_boundaries USING INDEX sqlite_autoindex_episode_asset_boundaries_1 (item_id=? AND asset_id=?)", "", "sqlite_autoindex_episode_asset_boundaries_1", []iaExtraKey{{"item_id", "INTEGER"}, {"asset_id", "TEXT"}}, []iaExtraKey{{"item", "INTEGER"}, {"asset", "TEXT"}}},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.extra." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA extra octets require the exact closed point definition", func(d stmt.Definition) bool {
			primary, indexes := len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == v.pk && len(d.Plan.Indexes) == 0, len(d.Plan.Indexes) == 1 && d.Plan.Indexes[0] == v.index && len(d.Plan.PrimaryKeys) == 0
			if v.pk == "" {
				primary = len(d.Plan.PrimaryKeys) == 0
			}
			if v.index == "" {
				indexes = len(d.Plan.Indexes) == 0
			}
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || !(primary && indexes) || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != iaExtraSeek(v.table) || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			if len(d.Params) != len(v.params) {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i].name || p.Nullable || p.Type != iaExtraParamType(v.params[i].kind) {
					return false
				}
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error {
			return auditIAExtraOctets(ctx, c, v.table, v.keys)
		}, func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != v.eqp {
				return fmt.Errorf("IA extra octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}

func iaExtraParamType(kind string) stmt.Type {
	if kind == "TEXT" {
		return stmt.Text
	}
	return stmt.Integer
}

func iaExtraSeek(table string) string {
	if table == "episode_asset_boundaries" {
		return "item_id=? AND asset_id=?"
	}
	return "rowid=?"
}

// auditIAExtraOctets proves the unshadowed native octet_length and the exact
// native primary key. A different key or a rewritten key order is refused.
func auditIAExtraOctets(ctx context.Context, c *sql.Conn, table string, keys []iaExtraKey) error {
	functions, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IA extra: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA extra: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA extra: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA extra: native octet_length missing")
	}
	columns, e := query(ctx, c, "PRAGMA main.table_xinfo("+table+")")
	if e != nil {
		return e
	}
	found := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("IA extra: invalid native columns")
		}
		n, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("IA extra: invalid native key metadata")
		}
		if n <= 0 {
			continue
		}
		if n > int64(len(keys)) || row[1] != keys[n-1].name || row[2] != keys[n-1].kind || row[6] != int64(0) {
			return fmt.Errorf("IA extra: exact native primary key required")
		}
		found++
	}
	if found != len(keys) {
		return fmt.Errorf("IA extra: native primary key missing")
	}
	return nil
}
