package plan

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// MP reading source selection owns its closed per-column octet points here. The
// SQL bytes are deliberately repeated: plan's registry is the authority, and a
// drift between the read statement and this preflight fails Capture instead of
// silently pairing a different projection.
const (
	mpReadingPublicationOctetsID stmt.ID = "read.browse.mp.reading.publication_octets"
	mpReadingObjectOctetsID      stmt.ID = "read.browse.mp.reading.object_octets"
	mpReadingLocalObjectOctetsID stmt.ID = "read.browse.mp.reading.local_object_octets"
)

const mpReadingPublicationOctetsSQL = `SELECT octet_length(p.format),octet_length(a.token) FROM reading_publications AS p INDEXED BY sqlite_autoindex_reading_publications_2 CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.entity_id=p.item_id AND l.asset_id=p.asset_id CROSS JOIN catalog_assets AS a ON a.id=p.asset_id WHERE p.item_id=? AND p.asset_id=? AND a.library_id=? AND l.library_id=a.library_id`
const mpReadingObjectOctetsBase = `SELECT octet_length(o.id),octet_length(o.asset_id),octet_length(o.source_id),octet_length(o.root_incarnation),octet_length(o.relative_path),octet_length(o.object_identity),octet_length(o.revision),octet_length(o.evidence_json),octet_length(o.state),octet_length(o.unsupported_reason),octet_length(s.id),octet_length(s.library_id),octet_length(s.kind),octet_length(s.incarnation),octet_length(s.root_identity),octet_length(s.health) FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.enabled=1 AND s.health NOT IN('offline','root_changed','removing')`
const mpReadingObjectOctetsSQL = mpReadingObjectOctetsBase + ` ORDER BY o.id LIMIT ?`
const mpReadingLocalObjectOctetsSQL = mpReadingObjectOctetsBase + ` AND s.kind='local' ORDER BY o.id LIMIT ?`

const mpReadingObjectIndex = "inventory_objects_reading_asset_object"

var (
	mpReadingPublicationAuto = stmt.AutoIndexBinding{Table: "reading_publications", Name: "sqlite_autoindex_reading_publications_2", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "item_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}
	mpReadingLinkAuto        = stmt.AutoIndexBinding{Table: "catalog_asset_links", Name: "sqlite_autoindex_catalog_asset_links_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "entity_id", Collation: "BINARY"}, {Name: "asset_id", Collation: "BINARY"}}}
	mpReadingSourceAuto      = stmt.AutoIndexBinding{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
)

func init() {
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	publicationParams := []stmt.Param{integer("item"), integer("asset"), integer("library")}
	objectParams := []stmt.Param{text("token"), text("library"), integer("size"), integer("modified"), integer("limit")}
	publicationPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingPublicationAuto.Name, mpReadingLinkAuto.Name}, PrimaryKeys: []string{"a"}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingPublicationAuto, mpReadingLinkAuto}}
	objectPlan := stmt.Plan{Hot: true, Indexes: []string{mpReadingObjectIndex, mpReadingSourceAuto.Name}, AutoIndexes: []stmt.AutoIndexBinding{mpReadingSourceAuto}}

	pointOctets.add(octetShape{mpReadingPublicationOctetsID, mpReadingPublicationOctetsSQL,
		"MP reading publication bytes require the exact closed source point",
		func(d stmt.Definition) bool {
			return mpReadingOctetDefinition(d, mpReadingPublicationOctetsID, mpReadingPublicationOctetsSQL, publicationParams, publicationPlan)
		},
		auditMPReadingPublicationOctets, checkMPReadingPublicationOctets})
	pointOctets.add(octetShape{mpReadingObjectOctetsID, mpReadingObjectOctetsSQL,
		"MP reading object bytes require the exact canonical object point",
		func(d stmt.Definition) bool {
			return mpReadingOctetDefinition(d, mpReadingObjectOctetsID, mpReadingObjectOctetsSQL, objectParams, objectPlan)
		},
		auditMPReadingObjectOctets, checkMPReadingObjectOctets})
	pointOctets.add(octetShape{mpReadingLocalObjectOctetsID, mpReadingLocalObjectOctetsSQL,
		"MP reading local archive bytes require the exact local object point",
		func(d stmt.Definition) bool {
			return mpReadingOctetDefinition(d, mpReadingLocalObjectOctetsID, mpReadingLocalObjectOctetsSQL, objectParams, objectPlan)
		},
		auditMPReadingObjectOctets, checkMPReadingObjectOctets})
}

func mpReadingOctetDefinition(d stmt.Definition, id stmt.ID, sql string, params []stmt.Param, p stmt.Plan) bool {
	return d.ID == id && d.SQL == sql && d.Kind == stmt.Read && d.Owner == "store/read MP" && len(d.CallSites) == 1 && reflect.DeepEqual(d.Params, params) && reflect.DeepEqual(d.Plan, p) && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0
}

func auditMPReadingPublicationOctets(ctx context.Context, c *sql.Conn) error {
	if err := auditClosedOctetKey(ctx, c, "reading_publications", mpReadingPublicationAuto.Name, []string{"item_id", "asset_id"}); err != nil {
		return err
	}
	if err := auditClosedOctetKey(ctx, c, "catalog_asset_links", mpReadingLinkAuto.Name, []string{"entity_id", "asset_id"}); err != nil {
		return err
	}
	return auditMPReadingAssetKeys(ctx, c)
}

// The asset is read by its integer primary key; the token is the UNIQUE identity
// that inventory_objects references, so both native keys are pinned.
func auditMPReadingAssetKeys(ctx context.Context, c *sql.Conn) error {
	info, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_assets)")
	if err != nil {
		return err
	}
	primary, columns := 0, 0
	for _, row := range info.Values {
		if len(row) != 7 {
			return fmt.Errorf("MP reading preflight: invalid asset column metadata")
		}
		columns++
		if position, ok := row[5].(int64); !ok {
			return fmt.Errorf("MP reading preflight: invalid asset key position")
		} else if position != 0 {
			if position != 1 || row[1] != "id" || row[2] != "INTEGER" || row[6] != int64(0) {
				return fmt.Errorf("MP reading preflight: exact integer asset identity required")
			}
			primary++
		}
	}
	if primary != 1 || columns == 0 {
		return fmt.Errorf("MP reading preflight: exact integer asset identity required")
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list(catalog_assets)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("MP reading preflight: invalid asset index metadata")
		}
		if row[1] == "sqlite_autoindex_catalog_assets_1" {
			origin, err := autoindexOrigin(row)
			if err != nil || origin != "u" {
				return fmt.Errorf("MP reading preflight: asset token must be a UNIQUE key")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MP reading preflight: pinned asset token key missing")
	}
	key, err := query(ctx, c, "PRAGMA main.index_info(sqlite_autoindex_catalog_assets_1)")
	if err != nil {
		return err
	}
	if len(key.Values) != 1 || len(key.Values[0]) != 3 || key.Values[0][2] != "token" {
		return fmt.Errorf("MP reading preflight: asset token key must be exactly token")
	}
	return nil
}

func auditMPReadingObjectOctets(ctx context.Context, c *sql.Conn) error {
	if err := auditClosedOctetKey(ctx, c, "library_sources", mpReadingSourceAuto.Name, []string{"id"}); err != nil {
		return err
	}
	if err := mainBaseTable(ctx, c, "inventory_objects"); err != nil {
		return err
	}
	indexes, err := query(ctx, c, "PRAGMA main.index_list(inventory_objects)")
	if err != nil {
		return err
	}
	found := false
	for _, row := range indexes.Values {
		if len(row) != 5 {
			return fmt.Errorf("MP reading preflight: invalid object index metadata")
		}
		if row[1] == mpReadingObjectIndex {
			if row[2] != int64(0) || row[3] != "c" || row[4] != int64(0) {
				return fmt.Errorf("MP reading preflight: plain nonpartial object index required")
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("MP reading preflight: pinned asset/object index missing")
	}
	key, err := query(ctx, c, "PRAGMA main.index_info("+mpReadingObjectIndex+")")
	if err != nil {
		return err
	}
	if len(key.Values) != 2 || len(key.Values[0]) != 3 || len(key.Values[1]) != 3 ||
		key.Values[0][0] != int64(0) || key.Values[1][0] != int64(1) ||
		key.Values[0][2] != "asset_id" || key.Values[1][2] != "id" {
		return fmt.Errorf("MP reading preflight: object index must be exactly asset_id,id")
	}
	return nil
}

func checkMPReadingPublicationOctets(eqp Rows) error {
	return mpReadingExactRows(eqp, "MP reading publication octets",
		"SEARCH p USING PRIMARY KEY (item_id=? AND asset_id=?)",
		"SEARCH l USING PRIMARY KEY (entity_id=? AND asset_id=?)",
		"SEARCH a USING INTEGER PRIMARY KEY (rowid=?)")
}

func checkMPReadingObjectOctets(eqp Rows) error {
	return mpReadingExactRows(eqp, "MP reading object octets",
		"SEARCH o USING INDEX "+mpReadingObjectIndex+" (asset_id=?)",
		"SEARCH s USING INDEX "+mpReadingSourceAuto.Name+" (id=?)")
}

func mpReadingExactRows(eqp Rows, label string, want ...string) error {
	if len(eqp.Values) != len(want) {
		return fmt.Errorf("%s: exact %d-row point SEARCH required", label, len(want))
	}
	for i, row := range eqp.Values {
		if len(row) != 4 || row[3] != want[i] {
			return fmt.Errorf("%s: exact point SEARCH required at row %d", label, i)
		}
	}
	return nil
}
