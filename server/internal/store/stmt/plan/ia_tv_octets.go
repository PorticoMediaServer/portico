package plan

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const iaTVEntityOctetsSQL = "SELECT octet_length(public_id),octet_length(title),octet_length(numbering),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?"

const iaTVSourceOctetsSQL = "SELECT octet_length(public_id),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?"
const iaTVAssetSourceOctetsSQL = "SELECT octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?"

func init() {
	for _, shape := range []struct {
		name, sql, table, seek string
		params                 []string
	}{
		{"entity", iaTVEntityOctetsSQL, "catalog_entities", "rowid=?", []string{"item", "library"}},
		{"source", iaTVSourceOctetsSQL, "catalog_entities", "rowid=?", []string{"item", "library"}},
		{"asset_source", iaTVAssetSourceOctetsSQL, "catalog_assets", "rowid=?", []string{"asset", "library"}},
	} {
		shape := shape
		pointOctets.add(octetShape{id: stmt.ID("read.foreground.ia.tv." + shape.name + "_octets"), sql: shape.sql, mismatch: "IA TV byte preflight requires the exact closed point definition",
			definition: func(d stmt.Definition) bool {
				if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(shape.params) || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != shape.table || len(d.Plan.Seeks) != 1 || d.Plan.Seeks[0] != shape.seek || len(d.Plan.Indexes) != 0 || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.Waivers) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
					return false
				}
				for i, p := range d.Params {
					if p.Name != shape.params[i] || p.Type != stmt.Integer || p.Nullable {
						return false
					}
				}
				return true
			},
			audit: func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, shape.table) },
			point: func(eqp Rows) error {
				key := "INTEGER PRIMARY KEY"
				if shape.name == "link" {
					key = "PRIMARY KEY"
				}
				want := "SEARCH " + shape.table + " USING " + key + " (" + shape.seek + ")"
				if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
					return fmt.Errorf("IA TV exact primary-key point required: %s", want)
				}
				return nil
			},
		})
	}
}
func auditIATVOctets(ctx context.Context, c *sql.Conn, table string) error {
	rows, e := query(ctx, c, "PRAGMA function_list")
	if e != nil {
		return e
	}
	matches := 0
	for _, r := range rows.Values {
		if len(r) != 6 {
			return fmt.Errorf("IA TV invalid function metadata")
		}
		name, ok := r[0].(string)
		arity, aok := r[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("IA TV invalid function metadata")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if r[1] != int64(1) || arity != 1 {
				return fmt.Errorf("IA TV unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("IA TV unshadowed native octet_length required")
	}
	rows, e = query(ctx, c, "PRAGMA main.table_xinfo("+table+")")
	if e != nil {
		return e
	}
	want := map[string]int64{"id": 1}
	if table == "catalog_asset_links" {
		want = map[string]int64{"entity_id": 1, "asset_id": 2}
	}
	found := 0
	for _, r := range rows.Values {
		if len(r) != 7 {
			return fmt.Errorf("IA TV invalid native column metadata")
		}
		if r[5] != int64(0) {
			pos, ok := want[r[1].(string)]
			if !ok || r[2] != "INTEGER" || r[5] != pos || r[6] != int64(0) {
				return fmt.Errorf("IA TV exact native primary key required")
			}
			found++
		}
	}
	if found != len(want) {
		return fmt.Errorf("IA TV native primary key missing")
	}
	return nil
}
