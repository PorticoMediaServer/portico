package plan

import (
	"fmt"
	"reflect"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Only these three byte-exact, point-key IA entry reads may project CASE.
// The reviewed projections contain comparisons, bound values and integer
// literals only: no function, subquery, alternate source or variable output.
// parserSQL removes only that fixed projection for source/key grammar audits.
// SQLite always compiles the ORIGINAL SQL; Capture still inspects the actual
// VM/EQP and MAIN/TEMP sources and requires the exact definition below. This
// does not admit arbitrary CASE syntax, a caller recipe or a lint/EQP waiver.
type iaEntryRecipe struct {
	id             stmt.ID
	sql, parserSQL string
	params         []stmt.Param
	plan           stmt.Plan
}

var iaEntryRecipes = []iaEntryRecipe{
	{id: "ia.entry.history_owner", sql: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,CASE WHEN original_library_id=? AND original_library_key=? AND command_kind=? AND original_request_digest=? AND original_source_id IS ? THEN 1 ELSE 0 END FROM catalog_configuration_operations WHERE original_operation_id=?`, parserSQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,0 FROM catalog_configuration_operations WHERE original_operation_id=?`, params: []stmt.Param{{Name: "library", Type: stmt.Integer}, {Name: "key", Type: stmt.Text}, {Name: "kind", Type: stmt.Text}, {Name: "request", Type: stmt.Blob}, {Name: "source", Type: stmt.Text, Nullable: true}, {Name: "operation", Type: stmt.Blob}}, plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_operations"}, Seeks: []string{"original_operation_id=?"}}},
	{id: "ia.entry.history_header", sql: `SELECT operation_id,request_digest,CASE command_kind WHEN 'configure_library' THEN 100 WHEN 'configure_source' THEN 101 ELSE 0 END,result_count,result_digest,result_version FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=?`, parserSQL: `SELECT operation_id,request_digest,0,result_count,result_digest,result_version FROM catalog_configuration_receipts WHERE original_operation_id=? AND command_incarnation=? AND command_sequence=?`, params: []stmt.Param{{Name: "operation", Type: stmt.Blob}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}}, plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_configuration_receipts"}, Seeks: []string{"original_operation_id=? AND command_incarnation=? AND command_sequence=?"}}},
	{id: "ia.entry.namespace", sql: `SELECT CASE WHEN library_id=? THEN 1 ELSE 0 END,retired FROM catalog_libraries WHERE id=?`, parserSQL: `SELECT 0,retired FROM catalog_libraries WHERE id=?`, params: []stmt.Param{{Name: "key", Type: stmt.Text}, {Name: "library", Type: stmt.Integer}}, plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}},
}

func iaEntryParserSQL(sql string) string {
	for _, recipe := range iaEntryRecipes {
		if sql == recipe.sql {
			return recipe.parserSQL
		}
	}
	return sql
}

func checkIAEntryDefinition(d stmt.Definition) error {
	for _, recipe := range iaEntryRecipes {
		if d.ID == recipe.id || d.SQL == recipe.sql {
			if d.ID != recipe.id || d.SQL != recipe.sql || d.Kind != stmt.Read || !reflect.DeepEqual(d.Params, recipe.params) || !reflect.DeepEqual(d.Plan, recipe.plan) {
				return fmt.Errorf("plan %s: IA entry projection requires its exact compiled ID/SQL/parameters/point plan", d.ID)
			}
			return nil
		}
	}
	return nil
}
