package plan

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func init() {
	for _, family := range []string{"album", "book"} {
		pointOctets.add(octetShape{stmt.ID("read.browse.rs.children." + family + ".sort_octets"), "SELECT octet_length(e.sort_key) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e." + family + "_id=?", "RS child ordering requires its exact entity and membership point", rsDetailOctetDefinition, auditRSDetailOctetPoint, checkRSDetailOctetPoint})
	}
}
