package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rsArtOctetsSQL = `SELECT octet_length(a.role),octet_length(a.choice),octet_length(a.digest),octet_length(own.public_id),octet_length(sb.digest),octet_length(sb.mime),octet_length(lb.digest),octet_length(lb.mime),octet_length(sb.status),octet_length(lb.status),octet_length(a.source_fence),octet_length(a.source_object_id),octet_length(a.source_object_revision),octet_length(o.id),octet_length(o.revision),octet_length(o.root_incarnation),octet_length(ls.incarnation),octet_length(ls.library_id),octet_length(cl.library_id)
FROM artwork_selections AS a NOT INDEXED
LEFT JOIN catalog_entities AS own NOT INDEXED ON own.id=a.entity_id
LEFT JOIN catalog_libraries AS cl NOT INDEXED ON cl.id=own.library_id
LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest
LEFT JOIN artwork_blobs AS sb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON sb.digest=s.small_blob
LEFT JOIN artwork_blobs AS lb INDEXED BY sqlite_autoindex_artwork_blobs_1 ON lb.digest=s.large_blob
LEFT JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.source_object_id
LEFT JOIN library_sources AS ls INDEXED BY sqlite_autoindex_library_sources_1 ON ls.id=o.source_id
WHERE a.kind='entity' AND a.entity_id=? AND a.role=? ORDER BY a.choice LIMIT 1`

func init() {
	pointOctets.add(octetShape{"read.browse.rs.art.octets", rsArtOctetsSQL, "RS art requires exact single-role choice/source points", rsArtOctetDefinition, auditRSArtOctetPoint, checkRSArtOctetPoint})
}
func rsArtOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || d.Params[0].Type != stmt.Integer || d.Params[1].Type != stmt.Text || d.Params[0].Nullable || d.Params[1].Nullable || len(d.Plan.PrimaryKeys) != 3 || d.Plan.PrimaryKeys[0] != "a" || d.Plan.PrimaryKeys[1] != "own" || d.Plan.PrimaryKeys[2] != "cl" || len(d.Plan.Indexes) != 4 || len(d.Plan.AutoIndexes) != 4 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	return true
}
func auditRSArtOctetPoint(ctx context.Context, c *sql.Conn) error {
	return auditRSDetailOctetPoint(ctx, c)
}
func checkRSArtOctetPoint(eqp Rows) error {
	want := []string{"SEARCH a USING PRIMARY KEY (kind=? AND entity_id=? AND role=?)", "SEARCH own USING INTEGER PRIMARY KEY (rowid=?) LEFT-JOIN", "SEARCH cl USING INTEGER PRIMARY KEY (rowid=?) LEFT-JOIN", "SEARCH s USING INDEX sqlite_autoindex_artwork_sources_1 (digest=?) LEFT-JOIN", "SEARCH sb USING INDEX sqlite_autoindex_artwork_blobs_1 (digest=?) LEFT-JOIN", "SEARCH lb USING INDEX sqlite_autoindex_artwork_blobs_1 (digest=?) LEFT-JOIN", "SEARCH o USING INDEX sqlite_autoindex_inventory_objects_1 (id=?) LEFT-JOIN", "SEARCH ls USING INDEX sqlite_autoindex_library_sources_1 (id=?) LEFT-JOIN"}
	if len(eqp.Values) != len(want) {
		return fmt.Errorf("RS art: exact source points required")
	}
	for n, row := range eqp.Values {
		if len(row) != 4 || row[3] != want[n] {
			return fmt.Errorf("RS art: point%d differs: %v", n, row)
		}
	}
	return nil
}
