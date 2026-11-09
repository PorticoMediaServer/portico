package plan

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Saved COLLECTION directory payloads are measured at one exact native point:
// the union resource rowid, the share rowid, the saved entry rowid constrained
// by resource id, the resource metadata key and the member entity rowid
// constrained by library and kind. Union enumeration itself returns fixed
// numeric rowids only, so a raw resource name is never a preflighted payload.
const rsSavedDirectoryResourceOctetsSQL = `SELECT octet_length(r.id),octet_length(r.name) FROM saved_resources AS r WHERE r.rowid=? AND r.kind='collection' AND r.deleted=0`
const rsSavedDirectoryShareResourceOctetsSQL = `SELECT octet_length(s.resource_id) FROM saved_resource_shares AS s WHERE s.rowid=?`
const rsSavedDirectoryEntryOctetsSQL = `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedDirectoryMetadataOctetsSQL = `SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.created_at),octet_length(r.visibility) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`

// The octets statement measures the same member point as the card payload.
// IFNULL(p.item_id,e.id)=e.id is always true for the joined row and keeps the
// optional personal LEFT JOIN in the measured statement, so SQLite cannot drop
// it and the declared personal_current_live access path stays real.
const rsSavedDirectoryCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND IFNULL(p.item_id,e.id)=e.id`

func init() {
	for _, v := range []struct {
		name, sql, mismatch, index, seek string
		types                            []stmt.Type
		p                                stmt.Plan
	}{
		{"resource", rsSavedDirectoryResourceOctetsSQL, "saved directory resource requires one exact collection rowid point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Integer}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"share_resource", rsSavedDirectoryShareResourceOctetsSQL, "saved directory share resource requires one exact share rowid point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Integer}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"entry", rsSavedDirectoryEntryOctetsSQL, "saved directory entry requires one exact entry rowid point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Integer, stmt.Text}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"metadata", rsSavedDirectoryMetadataOctetsSQL, "saved directory metadata requires one exact collection resource point", "sqlite_autoindex_saved_resources_1", "id=?", []stmt.Type{stmt.Text}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}, AutoIndexes: rsSavedDirectoryMetadataAutoIndexes()}},
		{"card", rsSavedDirectoryCardOctetsSQL, "saved directory card requires one exact entity point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Text, stmt.Integer, stmt.Integer, stmt.Integer}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, PrimaryKeys: []string{"e"}}},
	} {
		pointOctets.add(octetShape{stmt.ID("read.browse.rs.saved.directory." + v.name + "_octets"), v.sql, v.mismatch, func(d stmt.Definition) bool {
			if d.Kind != stmt.Read || !reflect.DeepEqual(d.Plan, v.p) || len(d.Params) != len(v.types) || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
				return false
			}
			for n, p := range d.Params {
				if p.Nullable || p.Type != v.types[n] {
					return false
				}
			}
			return true
		}, auditRSDetailOctetPoint, func(r Rows) error {
			return checkRSCollectionCompleteOctetPoint(r, v.index, v.seek)
		}})
	}
}

func rsSavedDirectoryMetadataAutoIndexes() []stmt.AutoIndexBinding {
	return []stmt.AutoIndexBinding{{Table: "saved_resources", Name: "sqlite_autoindex_saved_resources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}
}
