package plan

import "fmt"
import "context"
import "database/sql"
import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Closed exact points for normalized related-source identity and selected cards.
func init() {
	pointOctets.add(octetShape{stmt.ID("read.browse.rs.related.card_octets"), `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(e.artwork_incarnation) FROM catalog_entities AS e WHERE e.id=?`, "RS related requires its exact entity point", func(d stmt.Definition) bool {
		return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 1 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "e" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, auditRSDetailOctetPoint, checkRSDetailOctetPoint})
}
func init() {
	pointOctets.add(octetShape{stmt.ID("read.browse.rs.related.credit_identity_octets"), `SELECT octet_length(provider),octet_length(provider_person_id) FROM catalog_credits WHERE image_id=? AND ord=?`, "RS related requires its exact credit point", func(d stmt.Definition) bool {
		return d.Kind == stmt.Read && d.Plan.Hot && len(d.Params) == 2 && d.Params[0].Type == stmt.Integer && !d.Params[0].Nullable && d.Params[1].Type == stmt.Integer && !d.Params[1].Nullable && len(d.Plan.PrimaryKeys) == 1 && d.Plan.PrimaryKeys[0] == "catalog_credits" && len(d.Plan.Indexes) == 0 && len(d.Plan.AutoIndexes) == 0 && len(d.Plan.Waivers) == 0 && len(d.Plan.AllowedScans) == 0 && d.Plan.NativeInsert == "" && d.Plan.NativeRetained == ""
	}, auditRSRelatedCreditOctetPoint, func(eqp Rows) error {
		if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH catalog_credits USING PRIMARY KEY (image_id=? AND ord=?)" {
			return fmt.Errorf("exact related credit point SEARCH required")
		}
		return nil
	}})
}

func auditRSRelatedCreditOctetPoint(ctx context.Context, c *sql.Conn) error {
	if err := auditRSDetailOctetPoint(ctx, c); err != nil {
		return err
	}
	info, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_credits)")
	if err != nil {
		return err
	}
	count := 0
	for _, r := range info.Values {
		if len(r) != 7 {
			return fmt.Errorf("related credit: native column metadata")
		}
		if r[5] != int64(0) {
			count++
			want := "image_id"
			if r[5] == int64(2) {
				want = "ord"
			} else if r[5] != int64(1) {
				return fmt.Errorf("related credit: unexpected PK")
			}
			if r[1] != want || r[2] != "INTEGER" || r[3] != int64(1) || r[6] != int64(0) {
				return fmt.Errorf("related credit: exact nonnullable integer PK required")
			}
		}
	}
	if count != 2 {
		return fmt.Errorf("related credit: exact composite PK required")
	}
	return nil
}
