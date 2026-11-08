package plan

import (
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rsDetailComposedItemOctetsID stmt.ID = "read.browse.rs.detail_composed.item_octets"
const rsDetailComposedItemOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview)
FROM catalog_entities AS e
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id
WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	pointOctets.add(octetShape{rsDetailComposedItemOctetsID, rsDetailComposedItemOctetsSQL, "Composed detail item requires its exact entity and current-profile personal point", rsDetailComposedItemDefinition, auditRSDetailOctetPoint, checkRSDetailComposedItemPoint})
}

func rsDetailComposedItemDefinition(d stmt.Definition) bool {
	// The length projection elides the unused personal LEFT JOIN, so this exact
	// entity point carries no index hint; the payload point keeps its exact
	// personal_current_live index.
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 4 || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "e" || len(d.Plan.Indexes) != 0 || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if d.Params[0].Type != stmt.Text || d.Params[0].Nullable {
		return false
	}
	for _, p := range d.Params[1:] {
		if p.Type != stmt.Integer || p.Nullable {
			return false
		}
	}
	return true
}

func checkRSDetailComposedItemPoint(eqp Rows) error {
	// The length projection never reads a personal column, so SQLite may elide
	// the unused LEFT JOIN. Either shape is the same exact entity point.
	if len(eqp.Values) < 1 || len(eqp.Values) > 2 || len(eqp.Values[0]) != 4 {
		return fmt.Errorf("composed detail item: exact entity/current-profile point required: %v", eqp.Values)
	}
	if eqp.Values[0][3] != "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("composed detail item: exact entity point required")
	}
	if len(eqp.Values) == 2 {
		if len(eqp.Values[1]) != 4 || eqp.Values[1][3] != "SEARCH p USING INDEX personal_current_live (profile_id=? AND item_id=?)" {
			return fmt.Errorf("composed detail item: exact current-profile personal index required")
		}
	}
	return nil
}
