package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const rsPageCreditOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(c.credited_name),octet_length(r.label),octet_length(d.label),octet_length(e.added_at) FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`

func init() {
	pointOctets.add(octetShape{"read.browse.rs.people.page.credit_octets", rsPageCreditOctetsSQL, "RS grouped credit requires its exact image/ordinal point", rsCreditOctetDefinition, auditRSDetailOctetPoint, checkRSCreditOctetPoint})
	pointOctets.add(octetShape{"read.browse.rs.people.page.portrait_octets", rsPagePortraitOctetsSQL, "RS portrait requires an exact person/choice point", rsPagePortraitDefinition, auditRSPersonOctetPoint, rsPagePortraitPoint})
}

const rsPagePortraitOctetsSQL = `SELECT octet_length(a.digest),octet_length(s.small_blob),octet_length(s.large_blob),octet_length(s.ambient_blob),octet_length(s.accepted_mime) FROM artwork_selections AS a LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest WHERE a.kind='person' AND a.entity_id=? AND a.role='portrait' AND a.choice=?`

func rsPagePortraitDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 2 || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "a" || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != "sqlite_autoindex_artwork_sources_1" || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	if d.Params[0].Type != stmt.Integer || d.Params[0].Nullable || d.Params[1].Type != stmt.Text || d.Params[1].Nullable {
		return false
	}
	p := d.Plan.AutoIndexes[0]
	return p.Table == "artwork_sources" && p.Name == "sqlite_autoindex_artwork_sources_1" && p.Origin == "pk" && len(p.Columns) == 1 && p.Columns[0] == (stmt.IndexColumn{Name: "digest", Collation: "BINARY"})
}
func rsPagePortraitPoint(eqp Rows) error {
	for _, r := range eqp.Values {
		if len(r) == 4 && r[3] == "SEARCH a USING PRIMARY KEY (kind=? AND entity_id=? AND role=? AND choice=?)" {
			return nil
		}
	}
	return fmt.Errorf("RS portrait: exact person/choice SEARCH required")
}
