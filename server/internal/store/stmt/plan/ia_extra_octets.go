package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaExtraOctetsSQL = `SELECT octet_length(library_id),octet_length(parent_path),octet_length(kind),octet_length(title),octet_length(asset_id),octet_length(relative_path) FROM item_extras WHERE item_id=?`

func init() {
	initIAExtraEntity()
	pointOctets.add(octetShape{"read.foreground.ia.extra.extra_octets", iaExtraOctetsSQL, "IA extra requires exact native point", func(d stmt.Definition) bool {
		return d.ID == "read.foreground.ia.extra.extra_octets" && d.SQL == iaExtraOctetsSQL && d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 1 && d.Params[0].Name == "item" && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "item_extras" && len(d.Plan.Seeks) == 1 && d.Plan.Seeks[0] == "rowid=?" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.Waivers) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, func(ctx context.Context, c *sql.Conn) error { return auditIAExtraOctets(ctx, c, "item_extras") }, func(eqp Rows) error {
		if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH item_extras USING INTEGER PRIMARY KEY (rowid=?)" {
			return fmt.Errorf("IA extra requires point SEARCH")
		}
		return nil
	}})
}
func auditIAExtraOctets(ctx context.Context, c *sql.Conn, table string) error {
	functions, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, row := range functions.Values {
		if len(row) != 6 {
			return fmt.Errorf("IA audio: invalid native functions")
		}
		name, ok := row[0].(string)
		arity, aok := row[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA audio: invalid native function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if row[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA audio: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA audio: native octet_length missing")
	}
	columns, e := query(ctx, c, "PRAGMA main.table_xinfo("+table+")")
	if e != nil {
		return e
	}
	want := []string{"item_id"}
	if table == "catalog_entities" {
		want = []string{"id"}
	}
	if table == "catalog_asset_links" {
		want = []string{"entity_id", "asset_id"}
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("IA audio: invalid native columns")
		}
		n, ok := row[5].(int64)
		if !ok {
			return fmt.Errorf("IA audio: invalid native key metadata")
		}
		if n > 0 {
			if n > int64(len(want)) || row[1] != want[n-1] || row[2] != "INTEGER" || row[6] != int64(0) {
				return fmt.Errorf("IA audio: exact native primary key required")
			}
			keys++
		}
	}
	if keys != len(want) {
		return fmt.Errorf("IA audio: native primary key missing")
	}
	return nil
}

const iaExtraEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation),octet_length(music_incarnation),octet_length(artwork_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`

func initIAExtraEntity() {
	for _, v := range []struct {
		name, sql, table, seek string
		params                 []string
	}{{"entity", iaExtraEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"entity", "library"}}} {
		v := v
		id := stmt.ID("read.foreground.ia.extra." + v.name + "_octets")
		if v.name == "link" {
			id = "read.foreground.ia.link_octets"
		}
		pointOctets.add(octetShape{id, v.sql, "IA audio octets require the exact closed point definition", func(d stmt.Definition) bool {
			if d.ID != id || d.SQL != v.sql || d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(v.params) || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != v.table || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != v.seek || len(d.Plan.Indexes) != 0 || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			for i, p := range d.Params {
				if p.Name != v.params[i] || p.Type != stmt.Integer || p.Nullable {
					return false
				}
			}
			return true
		}, func(ctx context.Context, c *sql.Conn) error { return auditIAExtraOctets(ctx, c, v.table) }, func(eqp Rows) error {
			want := "SEARCH " + v.table + " USING INTEGER PRIMARY KEY (rowid=?)"
			if v.name == "link" {
				want = "SEARCH catalog_asset_links USING PRIMARY KEY (entity_id=? AND asset_id=?)"
			}
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
				return fmt.Errorf("IA audio octets require exact point SEARCH: %s", want)
			}
			return nil
		}})
	}
}
