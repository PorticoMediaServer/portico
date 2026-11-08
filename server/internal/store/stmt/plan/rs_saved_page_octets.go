package plan

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Each variable payload is measured at a single native rowid/unique-ID point.
// Enumeration itself returns fixed numeric positions, including hidden rows.
func init() {
	for _, v := range []struct {
		name, sql, alias, index string
		types                   []stmt.Type
		p                       stmt.Plan
	}{
		{"resource", `SELECT octet_length(r.id),octet_length(r.name) FROM saved_resources AS r WHERE r.rowid=?`, "r", "", []stmt.Type{stmt.Integer}, stmt.Plan{Hot: true, PrimaryKeys: []string{"r"}}},
		{"share_resource", `SELECT octet_length(s.resource_id) FROM saved_resource_shares AS s WHERE s.rowid=?`, "s", "", []stmt.Type{stmt.Integer}, stmt.Plan{Hot: true, PrimaryKeys: []string{"s"}}},
		{"entry", `SELECT octet_length(e.id),octet_length(e.order_key) FROM saved_resource_entries AS e WHERE e.rowid=? AND e.resource_id=?`, "e", "", []stmt.Type{stmt.Integer, stmt.Text}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{"metadata", `SELECT octet_length(r.name),octet_length(r.summary),octet_length(r.created_at),octet_length(r.visibility) FROM saved_resources AS r INDEXED BY sqlite_autoindex_saved_resources_1 WHERE r.id=? AND r.kind='collection' AND r.deleted=0`, "r", "sqlite_autoindex_saved_resources_1", []stmt.Type{stmt.Text}, rsSavedMetadataPointPlan()},
		{"card", `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview) FROM catalog_entities AS e LEFT JOIN personal_items AS p ON p.profile_id=? AND p.item_id=e.id WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`, "e", "", []stmt.Type{stmt.Text, stmt.Integer, stmt.Integer, stmt.Integer}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
	} {
		pointOctets.add(octetShape{stmt.ID("read.browse.rs.saved_page." + v.name + "_octets"), v.sql, "saved collection payload requires a single exact native point", func(d stmt.Definition) bool {
			if d.Kind != stmt.Read || !reflect.DeepEqual(d.Plan, v.p) || len(d.Params) != len(v.types) {
				return false
			}
			for n, p := range d.Params {
				if p.Nullable || p.Type != v.types[n] {
					return false
				}
			}
			return true
		}, auditRSDetailOctetPoint, func(r Rows) error {
			found := false
			for _, row := range r.Values {
				if len(row) != 4 {
					return fmt.Errorf("saved point: invalid EQP")
				}
				s, ok := row[3].(string)
				if !ok || !strings.HasPrefix(s, "SEARCH ") {
					return fmt.Errorf("saved point: SEARCH required")
				}
				if v.index == "" && s == "SEARCH "+v.alias+" USING INTEGER PRIMARY KEY (rowid=?)" {
					found = true
				}
				if v.index != "" && s == "SEARCH "+v.alias+" USING INDEX "+v.index+" (id=?)" {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("saved point: exact point seek absent")
			}
			return nil
		}})
	}
}
func rsSavedMetadataPointPlan() stmt.Plan {
	return stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_saved_resources_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "saved_resources", Name: "sqlite_autoindex_saved_resources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
}
