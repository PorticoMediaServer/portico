package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These exact points are the read-side TV preparation twins. They add no
// registration API: each shape is a closed point with its own native
// function/key/EQP audit.
const iaTVEntityOctetsSQL = `SELECT octet_length(public_id),octet_length(title),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`
const iaTVLinkOctetsSQL = `SELECT octet_length(edition),octet_length(playback_origin_incarnation) FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`
const iaTVAssetOctetsSQL = `SELECT octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?`

func init() {
	type shape struct {
		name, sql, table, seek string
		params                 []string
		eqp                    string
	}
	for _, v := range []shape{
		{"show", iaTVEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"item", "library"}, "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)"},
		{"season", iaTVEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"item", "library"}, "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)"},
		{"episode", iaTVEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"item", "library"}, "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)"},
		{"link", iaTVLinkOctetsSQL, "catalog_asset_links", "entity_id=? AND asset_id=?", []string{"item", "asset", "library"}, "SEARCH catalog_asset_links USING PRIMARY KEY (entity_id=? AND asset_id=?)"},
		{"asset", iaTVAssetOctetsSQL, "catalog_assets", "rowid=?", []string{"asset", "library"}, "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)"},
	} {
		v := v
		id := stmt.ID("read.foreground.ia.tv." + v.name + "_octets")
		pointOctets.add(octetShape{id, v.sql, "IA TV octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(v.params) || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != v.seek || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i] || p.Type != stmt.Integer || p.Nullable {
					return false
				}
			}
			if v.name == "link" {
				if len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "catalog_asset_links" || len(d.Plan.Indexes) != 0 || len(d.Params) != 3 {
					return false
				}
			} else if len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != v.table || len(d.Plan.Indexes) != 0 {
				return false
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error {
			return auditIATVOctets(ctx, c, v.table)
		}, func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != v.eqp {
				return fmt.Errorf("IA TV octets require exact point SEARCH: %s", v.eqp)
			}
			return nil
		}})
	}
}

// auditIATVOctets proves the one unshadowed native octet_length and the exact
// native primary key. catalog_entities and catalog_assets use the INTEGER
// rowid; catalog_asset_links is a WITHOUT ROWID pair key.
func auditIATVOctets(ctx context.Context, c *sql.Conn, table string) error {
	functions, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IA TV: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA TV: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA TV: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA TV: native octet_length missing")
	}
	columns, e := query(ctx, c, "PRAGMA main.table_xinfo("+table+")")
	if e != nil {
		return e
	}
	want := map[string]int64{"id": 1}
	if table == "catalog_asset_links" {
		want = map[string]int64{"entity_id": 1, "asset_id": 2}
	}
	found := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("IA TV: invalid native columns")
		}
		if row[5] == int64(0) {
			continue
		}
		pos, ok := want[row[1].(string)]
		if !ok || row[2] != "INTEGER" || row[5] != pos || row[6] != int64(0) {
			return fmt.Errorf("IA TV: exact native primary key required")
		}
		found++
	}
	if found != len(want) {
		return fmt.Errorf("IA TV: native primary key missing")
	}
	return nil
}
