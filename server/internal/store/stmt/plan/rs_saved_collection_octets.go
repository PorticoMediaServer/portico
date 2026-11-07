package plan

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsSavedCollectionMetadataOctets = `SELECT octet_length(name),octet_length(summary),octet_length(created_at) FROM saved_resources INDEXED BY sqlite_autoindex_saved_resources_1 WHERE id=? AND kind='collection' AND deleted=0`

func init() {
	pointOctets.add(octetShape{"read.browse.rs.saved_collection.metadata_octets", rsSavedCollectionMetadataOctets, "saved collection metadata requires one exact native resource point", func(d stmt.Definition) bool {
		return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 1 && d.Params[0].Type == stmt.Text && !d.Params[0].Nullable && len(d.Plan.PrimaryKeys) == 0 && len(d.Plan.Indexes) == 1 && d.Plan.Indexes[0] == "sqlite_autoindex_saved_resources_1" && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && len(d.Plan.AutoIndexes) == 1 && d.Plan.AutoIndexes[0].Table == "saved_resources" && d.Plan.AutoIndexes[0].Name == "sqlite_autoindex_saved_resources_1" && d.Plan.AutoIndexes[0].Origin == "pk" && len(d.Plan.AutoIndexes[0].Columns) == 1 && d.Plan.AutoIndexes[0].Columns[0] == (stmt.IndexColumn{Name: "id", Collation: "BINARY"}) && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionOctetPoint(r, "sqlite_autoindex_saved_resources_1", "id=?")
	}})
}
