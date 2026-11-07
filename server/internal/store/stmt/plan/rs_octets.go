package plan

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"strings"
)

const rsDetailFactsOctetsID stmt.ID = "read.browse.rs.detail.facts_octets"
const rsDetailFactsOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview),octet_length(e.original_title),octet_length(e.tagline),octet_length(e.edition),octet_length(e.release_date),octet_length(e.content_rating),octet_length(e.studio),octet_length(e.network),octet_length(e.country),octet_length(e.added_at),octet_length(e.last_content_added_at),octet_length(e.show_ranges),octet_length(e.numbering),octet_length(e.local_identity_status),octet_length(e.ordering_basis),octet_length(e.record_label),octet_length(e.release_country),octet_length(e.edition_key),octet_length(e.author),octet_length(e.narrator),octet_length(e.series),octet_length(e.series_position),octet_length(e.collection_name_key) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	pointOctets.add(octetShape{rsDetailFactsOctetsID, rsDetailFactsOctetsSQL, "RS detail requires its exact entity point", rsDetailOctetDefinition, auditRSDetailOctetPoint, checkRSDetailOctetPoint})
}
func rsDetailOctetDefinition(d stmt.Definition) bool {
	if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != 3 || len(d.Plan.PrimaryKeys) != 1 || d.Plan.PrimaryKeys[0] != "e" || len(d.Plan.Indexes) != 0 || len(d.Plan.AutoIndexes) != 0 || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
		return false
	}
	for _, p := range d.Params {
		if p.Type != stmt.Integer || p.Nullable {
			return false
		}
	}
	return true
}
func auditRSDetailOctetPoint(ctx context.Context, c *sql.Conn) error {
	functions, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	matches := 0
	for _, r := range functions.Values {
		if len(r) != 6 {
			return fmt.Errorf("RS detail: invalid function metadata")
		}
		name, ok := r[0].(string)
		arity, aok := r[4].(int64)
		if !ok || !aok {
			return fmt.Errorf("RS detail: invalid function identity")
		}
		if strings.EqualFold(name, "octet_length") && (arity == 1 || arity == -1) {
			if r[1] != int64(1) || arity != 1 {
				return fmt.Errorf("RS detail: unshadowed native octet_length required")
			}
			matches++
		}
	}
	if matches != 1 {
		return fmt.Errorf("RS detail: exact native octet_length required")
	}
	info, err := query(ctx, c, "PRAGMA main.table_xinfo(catalog_entities)")
	if err != nil {
		return err
	}
	found := false
	for _, r := range info.Values {
		if len(r) != 7 {
			return fmt.Errorf("RS detail: invalid entity columns")
		}
		if r[1] == "id" {
			found = r[2] == "INTEGER" && r[5] == int64(1) && r[6] == int64(0)
		}
	}
	if !found {
		return fmt.Errorf("RS detail: exact integer identity required")
	}
	return nil
}
func checkRSDetailOctetPoint(eqp Rows) error {
	if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != "SEARCH e USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("RS detail: exact entity point SEARCH required")
	}
	return nil
}
