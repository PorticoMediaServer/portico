package plan

import (
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Saved COLLECTION member payloads are measured at one exact native point:
// the resource metadata key, the saved entry rowid constrained by resource id,
// and the member entity rowid constrained by library and kind. Enumeration
// itself returns fixed numeric rowids only.
const rsSavedMembersMetadataOctetsSQL = `SELECT octet_length(name),octet_length(summary),octet_length(created_at),octet_length(visibility) FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`
const rsSavedMembersEntryOctetsSQL = `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`
const rsSavedMembersCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	for _, v := range []struct {
		name, sql, mismatch, index, seek string
		types                            []stmt.Type
		p                                stmt.Plan
	}{
		{"metadata", rsSavedMembersMetadataOctetsSQL, "saved member metadata requires one exact collection resource point", "sqlite_autoindex_saved_resources_1", "id=?", []stmt.Type{stmt.Text}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}, AutoIndexes: rsSavedMetadataAutoIndexes()}},
		{"entry", rsSavedMembersEntryOctetsSQL, "saved member entry requires one exact entry rowid point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Integer, stmt.Text}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"card", rsSavedMembersCardOctetsSQL, "saved member card requires one exact entity point", "INTEGER PRIMARY KEY", "rowid=?", []stmt.Type{stmt.Text, stmt.Integer, stmt.Integer, stmt.Integer}, stmt.Plan{Hot: true, Indexes: []string{"personal_current_live"}, PrimaryKeys: []string{"e"}}},
	} {
		pointOctets.add(octetShape{stmt.ID("read.browse.rs.saved.members." + v.name + "_octets"), v.sql, v.mismatch, func(d stmt.Definition) bool {
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

func rsSavedMetadataAutoIndexes() []stmt.AutoIndexBinding {
	return []stmt.AutoIndexBinding{{Table: "saved_resources", Name: "sqlite_autoindex_saved_resources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}
}
