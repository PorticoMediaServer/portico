package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

// Complete authored collection positions are closed octet shapes: the unique
// (library_id,id) entity point and the two keyset legs of the authored order.
// Each returns one indexed position per row and never measures a whole cohort.
const rsCollectionCompletePointOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title) FROM catalog_entities AS e INDEXED BY catalog_entities_library_id WHERE e.library_id=? AND e.id=?`
const rsCollectionCompleteMemberOctetsSQL = `SELECT octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key=? AND m.item_id>? ORDER BY m.order_key,m.item_id LIMIT ?`
const rsCollectionCompleteMemberNextOctetsSQL = `SELECT octet_length(m.order_key) FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page WHERE m.collection_id=? AND m.order_key>? ORDER BY m.order_key,m.item_id LIMIT ?`

func init() {
	pointOctets.add(octetShape{"read.browse.rs.collection_complete.point_octets", rsCollectionCompletePointOctetsSQL, "RS complete collection requires its exact card point", func(d stmt.Definition) bool {
		return rsCollectionCompleteOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Integer}, "catalog_entities_library_id")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionCompleteOctetPoint(r, "catalog_entities_library_id", "library_id=? AND id=?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.collection_complete.member_octets", rsCollectionCompleteMemberOctetsSQL, "RS complete collection requires its exact authored tail point", func(d stmt.Definition) bool {
		return rsCollectionCompleteOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer, stmt.Integer}, "catalog_collection_members_page")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionCompleteOctetPoint(r, "catalog_collection_members_page", "collection_id=? AND order_key=? AND item_id>?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.collection_complete.member_next_octets", rsCollectionCompleteMemberNextOctetsSQL, "RS complete collection requires its exact authored next point", func(d stmt.Definition) bool {
		return rsCollectionCompleteOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer}, "catalog_collection_members_page")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionCompleteOctetPoint(r, "catalog_collection_members_page", "collection_id=? AND order_key>?")
	}})
}

func rsCollectionCompleteOctetDefinition(d stmt.Definition, types []stmt.Type, index string) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(types) || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.AutoIndexes) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" || len(d.Plan.PrimaryKeys) != 0 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != index {
		return false
	}
	for n, p := range d.Params {
		if p.Nullable || p.Type != types[n] {
			return false
		}
	}
	return true
}

func checkRSCollectionCompleteOctetPoint(r Rows, index, seek string) error {
	found := false
	for _, row := range r.Values {
		if len(row) != 4 {
			return fmt.Errorf("RS complete collection: invalid native EQP")
		}
		s, ok := row[3].(string)
		if !ok || !strings.HasPrefix(s, "SEARCH ") {
			return fmt.Errorf("RS complete collection: exact indexed source position required")
		}
		if strings.Contains(s, index+" (") && strings.Contains(s, seek) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("RS complete collection: missing exact source seek")
	}
	return nil
}
