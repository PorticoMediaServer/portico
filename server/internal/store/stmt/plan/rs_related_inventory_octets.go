package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RS related availability contributes only its own exact-byte points to the
// merged closed registry. The source point shares its shape with the merged
// Reading inventory source point; the asset point is an integer asset PK point.
func init() {
	pointOctets.add(octetShape{
		"read.browse.rs.related.inventory.source_octets",
		`SELECT octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`,
		"RS related inventory requires the exact source point",
		func(d stmt.Definition) bool {
			if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != "sqlite_autoindex_library_sources_1" || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			for _, p := range d.Params {
				if p.Type != stmt.Text || p.Nullable {
					return false
				}
			}
			pin := d.Plan.AutoIndexes[0]
			return pin.Table == "library_sources" && pin.Name == "sqlite_autoindex_library_sources_1" && pin.Origin == "pk" && len(pin.Columns) == 1 && pin.Columns[0] == (stmt.IndexColumn{Name: "id", Collation: "BINARY"})
		},
		auditIssuerOctetKey,
		checkIssuerOctetPoint,
	})
	pointOctets.add(octetShape{
		"read.browse.rs.related.inventory.asset_octets",
		`SELECT octet_length(token),octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`,
		"RS related inventory requires the exact asset point",
		rsRelatedAssetOctetDefinition,
		auditRSRelatedAssetOctetKey,
		func(eqp Rows) error {
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("RS related inventory asset requires a native integer point")
			}
			return nil
		},
	})
}

func rsRelatedAssetOctetDefinition(d stmt.Definition) bool {
	return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && d.Params[1].Type == stmt.Integer && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "catalog_assets" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
}

// auditRSRelatedAssetOctetKey certifies the actual catalog_assets INTEGER
// primary-key alias after proving the native one-argument octet_length.
func auditRSRelatedAssetOctetKey(ctx context.Context, conn *sql.Conn) error {
	if err := auditBuiltinOctetLength(ctx, conn); err != nil {
		return err
	}
	columns, err := query(ctx, conn, "PRAGMA main.table_xinfo(catalog_assets)")
	if err != nil {
		return err
	}
	keys := 0
	for _, row := range columns.Values {
		if len(row) != 7 {
			return fmt.Errorf("RS related inventory asset: unexpected native column metadata")
		}
		if row[5] != int64(0) {
			if row[1] != "id" || row[2] != "INTEGER" || row[5] != int64(1) || row[6] != int64(0) {
				return fmt.Errorf("RS related inventory asset: native key must be exactly INTEGER id")
			}
			keys++
		}
	}
	if keys != 1 {
		return fmt.Errorf("RS related inventory asset: native INTEGER id key missing")
	}
	return nil
}
