package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const rsAutoIdentityOctetsSQL = `SELECT octet_length(a.provider),octet_length(a.provider_id) FROM catalog_automatic_collections AS a INDEXED BY catalog_automatic_collection_entity WHERE a.entity_id=? AND a.library_id=? AND a.removed=0 LIMIT 1`
const rsAutoEvidenceOctetsSQL = `SELECT octet_length(evidence_id),octet_length(name) FROM metadata_evidence_collections INDEXED BY metadata_evidence_collection_members WHERE provider=? AND collection_kind='collection' AND collection_id=? AND evidence_id>? ORDER BY evidence_id LIMIT 1`
const rsAuthoredTailOctetsSQL = `SELECT octet_length(order_key) FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key=? AND item_id>? ORDER BY order_key,item_id LIMIT 1`
const rsAuthoredNextOctetsSQL = `SELECT octet_length(order_key) FROM catalog_collection_members INDEXED BY catalog_collection_members_page WHERE collection_id=? AND order_key>? ORDER BY order_key,item_id LIMIT 1`
const rsCollectionCardOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title) FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind WHERE e.id=? AND e.library_id=? AND e.retired=0`

// Closed shapes return one indexed position. Evidence discovery measures its
// next TEXT cursor before decoding, never the entire provider cohort.
func init() {
	pointOctets.add(octetShape{"read.browse.rs.auto.identity_octets", rsAutoIdentityOctetsSQL, "RS collection requires its exact bounded source point", func(d stmt.Definition) bool {
		return rsCollectionOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Integer}, "catalog_automatic_collection_entity")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionOctetPoint(r, "catalog_automatic_collection_entity", "entity_id=?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.auto.evidence_octets", rsAutoEvidenceOctetsSQL, "RS collection requires its exact bounded source point", func(d stmt.Definition) bool {
		return rsCollectionOctetDefinition(d, []stmt.Type{stmt.Text, stmt.Text, stmt.Text}, "metadata_evidence_collection_members")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionOctetPoint(r, "metadata_evidence_collection_members", "provider=? AND collection_kind=? AND collection_id=? AND evidence_id>?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.auto.authored_tail_octets", rsAuthoredTailOctetsSQL, "RS collection requires its exact bounded source point", func(d stmt.Definition) bool {
		return rsCollectionOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Text, stmt.Integer}, "catalog_collection_members_page")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionOctetPoint(r, "catalog_collection_members_page", "collection_id=? AND order_key=? AND item_id>?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.auto.authored_next_octets", rsAuthoredNextOctetsSQL, "RS collection requires its exact bounded source point", func(d stmt.Definition) bool {
		return rsCollectionOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Text}, "catalog_collection_members_page")
	}, auditRSDetailOctetPoint, func(r Rows) error {
		return checkRSCollectionOctetPoint(r, "catalog_collection_members_page", "collection_id=? AND order_key>?")
	}})
	pointOctets.add(octetShape{"read.browse.rs.auto.card_octets", rsCollectionCardOctetsSQL, "RS collection requires its exact bounded source point", func(d stmt.Definition) bool {
		return rsCollectionOctetDefinition(d, []stmt.Type{stmt.Integer, stmt.Integer}, "")
	}, auditRSDetailOctetPoint, func(r Rows) error { return checkRSCollectionOctetPoint(r, "", "") }})
}
func rsCollectionOctetDefinition(d stmt.Definition, types []stmt.Type, index string) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(types) || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || len(d.Plan.AutoIndexes) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for n, p := range d.Params {
		if p.Nullable || p.Type != types[n] {
			return false
		}
	}
	if index == "" {
		return len(d.Plan.Indexes) == 0 && len(d.Plan.PrimaryKeys) == 2 && d.Plan.PrimaryKeys[0] == "e" && d.Plan.PrimaryKeys[1] == "k"
	}
	return len(d.Plan.Indexes) == 1 && d.Plan.Indexes[0] == index && len(d.Plan.PrimaryKeys) == 0
}
func checkRSCollectionOctetPoint(r Rows, index, seek string) error {
	found := false
	for _, row := range r.Values {
		if len(row) != 4 {
			return fmt.Errorf("RS collection: invalid native EQP")
		}
		s, ok := row[3].(string)
		if !ok || !strings.HasPrefix(s, "SEARCH ") {
			return fmt.Errorf("RS collection: exact indexed source position required")
		}
		if index == "" {
			if s == "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
				found = true
			}
		} else if strings.Contains(s, index+" (") && strings.Contains(s, seek) {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("RS collection: missing exact source seek")
	}
	return nil
}
