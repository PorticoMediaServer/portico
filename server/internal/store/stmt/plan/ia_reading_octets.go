package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
)

func init() {
	{
		id := stmt.ID("read.foreground.ia.reading.entity_octets")
		pointSQL := `SELECT octet_length(public_id),octet_length(title) FROM catalog_entities WHERE id=? AND library_id=?`
		pointOctets.add(octetShape{id, pointSQL, "IA reading exact closed point required", func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}})
		}, func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, "catalog_entities") }, func(r Rows) error {
			if len(r.Values) != 1 || r.Values[0][3] != "SEARCH catalog_entities USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("IA reading exact SEARCH required")
			}
			return nil
		}})
	}
	{
		id := stmt.ID("read.foreground.ia.reading.publication_octets")
		pointSQL := `SELECT octet_length(format),octet_length(path_object_id),octet_length(path_source_revision),octet_length(path_author),octet_length(path_author_source),octet_length(path_issue_date),octet_length(path_series),octet_length(path_series_source) FROM reading_publications WHERE item_id=? AND asset_id=?`
		pointOctets.add(octetShape{id, pointSQL, "IA reading exact closed point required", func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publications"}, Seeks: []string{"item_id=? AND asset_id=?"}})
		}, func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, "catalog_entities") }, func(r Rows) error {
			if len(r.Values) != 1 || r.Values[0][3] != "SEARCH reading_publications USING PRIMARY KEY (item_id=? AND asset_id=?)" {
				return fmt.Errorf("IA reading exact SEARCH required")
			}
			return nil
		}})
	}
	{
		id := stmt.ID("read.foreground.ia.reading.type_octets")
		pointSQL := `SELECT octet_length(publication_type),octet_length(source_format),octet_length(source_field),octet_length(inventory_object_id),octet_length(source_revision) FROM reading_publication_types WHERE item_id=? AND asset_id=?`
		pointOctets.add(octetShape{id, pointSQL, "IA reading exact closed point required", func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publication_types"}, Seeks: []string{"item_id=? AND asset_id=?"}})
		}, func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, "catalog_entities") }, func(r Rows) error {
			if len(r.Values) != 1 || r.Values[0][3] != "SEARCH reading_publication_types USING PRIMARY KEY (item_id=? AND asset_id=?)" {
				return fmt.Errorf("IA reading exact SEARCH required")
			}
			return nil
		}})
	}
	{
		id := stmt.ID("read.foreground.ia.reading.asset_octets")
		pointSQL := `SELECT octet_length(token) FROM catalog_assets WHERE id=? AND library_id=?`
		pointOctets.add(octetShape{id, pointSQL, "IA reading exact closed point required", func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}})
		}, func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, "catalog_entities") }, func(r Rows) error {
			if len(r.Values) != 1 || r.Values[0][3] != "SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)" {
				return fmt.Errorf("IA reading exact SEARCH required")
			}
			return nil
		}})
	}
	{
		id := stmt.ID("read.foreground.ia.reading.object_octets")
		pointSQL := `SELECT octet_length(source_id),octet_length(relative_path),octet_length(revision),octet_length(state),octet_length(asset_id) FROM inventory_objects INDEXED BY sqlite_autoindex_inventory_objects_1 WHERE id=?`
		pointOctets.add(octetShape{id, pointSQL, "IA reading exact closed point required", func(d stmt.Definition) bool {
			return d.Kind == stmt.Read && reflect.DeepEqual(d.Params, []stmt.Param{{Name: "object", Type: stmt.Text}}) && reflect.DeepEqual(d.Plan, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "inventory_objects", Name: "sqlite_autoindex_inventory_objects_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}, Seeks: []string{"id=?"}})
		}, func(ctx context.Context, c *sql.Conn) error { return auditIATVOctets(ctx, c, "catalog_entities") }, func(r Rows) error {
			if len(r.Values) != 1 || r.Values[0][3] != "SEARCH inventory_objects USING INDEX sqlite_autoindex_inventory_objects_1 (id=?)" {
				return fmt.Errorf("IA reading exact SEARCH required")
			}
			return nil
		}})
	}
}
