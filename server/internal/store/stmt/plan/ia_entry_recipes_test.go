package plan

import (
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestIAEntryRecipesRequireExactDefinition(t *testing.T) {
	for _, recipe := range iaEntryRecipes {
		t.Run(string(recipe.id), func(t *testing.T) {
			fresh := func() stmt.Definition {
				return stmt.Definition{ID: recipe.id, SQL: recipe.sql, Kind: stmt.Read, Params: append([]stmt.Param(nil), recipe.params...), Plan: recipe.plan}
			}
			if err := checkSyntax(fresh()); err != nil {
				t.Fatal("exact projection refused", err)
			}
			for name, mutate := range map[string]func(*stmt.Definition){
				"wrong_id":              func(d *stmt.Definition) { d.ID = "foreign.entry" },
				"changed_sql":           func(d *stmt.Definition) { d.SQL = "SELECT 1" },
				"write_kind":            func(d *stmt.Definition) { d.Kind = stmt.Write },
				"parameter_name":        func(d *stmt.Definition) { d.Params[0].Name = "foreign" },
				"parameter_type":        func(d *stmt.Definition) { d.Params[0].Type = stmt.Boolean },
				"parameter_nullability": func(d *stmt.Definition) { d.Params[0].Nullable = !d.Params[0].Nullable },
				"missing_parameter":     func(d *stmt.Definition) { d.Params = d.Params[1:] },
				"not_hot":               func(d *stmt.Definition) { d.Plan.Hot = false },
				"missing_key":           func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil },
				"wrong_seek":            func(d *stmt.Definition) { d.Plan.Seeks = []string{"foreign=?"} },
				"waiver": func(d *stmt.Definition) {
					d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "sql-lint", Form: "sql:CASE", Match: d.SQL, Reason: "not authorized"}}
				},
			} {
				t.Run(name, func(t *testing.T) {
					d := fresh()
					mutate(&d)
					err := checkSyntax(d)
					if err == nil {
						t.Fatal("changed projection definition accepted")
					}
					if !strings.Contains(err.Error(), "IA entry projection requires its exact compiled") {
						t.Fatal("definition fence was not the refusal", err)
					}
				})
			}
		})
	}
}

func TestIAEntryRecipesDoNotAdmitOtherCASE(t *testing.T) {
	for _, recipe := range iaEntryRecipes {
		for _, changed := range []string{
			strings.Replace(recipe.sql, "CASE", "CASE ", 1),
			strings.Replace(recipe.sql, "THEN 1", "THEN 2", 1),
			strings.Replace(recipe.sql, "CASE", "CASE (SELECT 1)", 1),
			recipe.sql + " UNION SELECT 1",
		} {
			if len(LintSQL(changed)) == 0 {
				t.Fatal("unregistered CASE grammar accepted", changed)
			}
		}
	}
	if len(LintSQL("SELECT CASE WHEN id=? THEN 1 ELSE 0 END FROM other WHERE id=?")) == 0 {
		t.Fatal("general CASE permission granted")
	}
}
