package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const rsPersonOctetsSQL = `SELECT octet_length(token),octet_length(name),octet_length(sort_name),octet_length(biography),octet_length(birth_place),octet_length(biography_source),octet_length(birth_date),octet_length(death_date) FROM catalog_people WHERE id=?`
const rsCreditOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(c.credited_name),octet_length(r.label),octet_length(d.label) FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`

func init() {
	pointOctets.add(octetShape{"read.browse.rs.people.person_octets", rsPersonOctetsSQL, "RS person requires its exact person point", rsPersonOctetDefinition, auditRSPersonOctetPoint, checkRSPersonOctetPoint})
	pointOctets.add(octetShape{"read.browse.rs.people.credit_octets", rsCreditOctetsSQL, "RS credit requires its exact image/ordinal point", rsCreditOctetDefinition, auditRSDetailOctetPoint, checkRSCreditOctetPoint})
}
func rsPersonOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 1 || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "catalog_people" || len(d.Plan.Indexes) != 0 || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	return d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable
}
func rsCreditOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 5 || len(d.Plan.PrimaryKeys) != 4 || len(d.Plan.Indexes) != 1 || d.Plan.Indexes[0] != "sqlite_autoindex_catalog_credits_1" || len(d.Plan.AutoIndexes) != 1 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for _, p := range d.Params {
		if p.Type != stmt.Integer || p.Nullable {
			return false
		}
	}
	for n, name := range []string{"e", "h", "r", "d"} {
		if d.Plan.PrimaryKeys[n] != name {
			return false
		}
	}
	pin := d.Plan.AutoIndexes[0]
	return pin.Table == "catalog_credits" && pin.Name == "sqlite_autoindex_catalog_credits_1" && pin.Origin == "pk" && len(pin.Columns) == 2 && pin.Columns[0] == (stmt.IndexColumn{Name: "image_id", Collation: "BINARY"}) && pin.Columns[1] == (stmt.IndexColumn{Name: "ord", Collation: "BINARY"})
}
func auditRSPersonOctetPoint(ctx context.Context, c *sql.Conn) error {
	// Reuse the exact native builtin audit already pinned by RS's entity points.
	if err := auditRSDetailOctetPoint(ctx, c); err != nil {
		return err
	}
	info, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_people)")
	if err != nil {
		return err
	}
	found := false
	for _, r := range info.Values {
		if len(r) != 7 {
			return fmt.Errorf("RS person: invalid columns")
		}
		if r[1] == "id" {
			found = r[2] == "INTEGER" && r[5] == int64(1) && r[6] == int64(0)
		}
	}
	if !found {
		return fmt.Errorf("RS person: exact integer identity required")
	}
	return nil
}
func checkRSPersonOctetPoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_people USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("RS person: exact person point SEARCH required")
	}
	return nil
}
func checkRSCreditOctetPoint(eqp Rows) error {
	found := false
	for _, r := range eqp.Values {
		if len(r) != 4 {
			return fmt.Errorf("RS credit: invalid native EQP")
		}
		s, ok := r[3].(string)
		if !ok {
			return fmt.Errorf("RS credit: invalid native EQP detail")
		}
		if strings.HasPrefix(s, "SEARCH c USING PRIMARY KEY (image_id=? AND ord=?)") {
			found = true
		}
	}
	if !found {
		return fmt.Errorf("RS credit: exact image/ordinal point SEARCH required")
	}
	return nil
}
