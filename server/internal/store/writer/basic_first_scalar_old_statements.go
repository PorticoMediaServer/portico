package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicFirstScalarOldKeys    stmt.ID = "scan.basic_first_scalar_old.keys"
	basicFirstScalarOldText    stmt.ID = "scan.basic_first_scalar_old.text"
	basicFirstScalarOldInteger stmt.ID = "scan.basic_first_scalar_old.integer"
	basicFirstScalarOldReal    stmt.ID = "scan.basic_first_scalar_old.real"
)

func basicFirstScalarOldDefinitions() []stmt.Definition {
	p := []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "field", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}}
	defs := []stmt.Definition{
		{ID: basicFirstScalarOldKeys, Kind: stmt.Read, SQL: `SELECT field,kind FROM catalog_info_scalar_old WHERE change_id=? ORDER BY field LIMIT 7`, Params: p[:1], Cohorts: []stmt.Cohort{{Name: "complete_keys", Bindings: []any{int64(51)}}}},
		{ID: basicFirstScalarOldText, Kind: stmt.Write, SQL: `DELETE FROM catalog_info_scalar_old WHERE change_id=? AND field=? AND kind=? AND old_text=? AND old_integer IS NULL AND old_real IS NULL RETURNING change_id,field,kind,old_integer,old_real,old_text`, Params: append(append([]stmt.Param{}, p...), stmt.Param{Name: "old_text", Type: stmt.Text}), Cohorts: []stmt.Cohort{{Name: "exact_text", Bindings: []any{int64(51), int64(4), int64(3), "old"}}}},
		{ID: basicFirstScalarOldInteger, Kind: stmt.Write, SQL: `DELETE FROM catalog_info_scalar_old WHERE change_id=? AND field=? AND kind=? AND old_integer=? AND old_real IS NULL AND old_text IS NULL RETURNING change_id,field,kind,old_integer,old_real,old_text`, Params: append(append([]stmt.Param{}, p...), stmt.Param{Name: "old_integer", Type: stmt.Integer}), Cohorts: []stmt.Cohort{{Name: "exact_integer", Bindings: []any{int64(51), int64(7), int64(1), int64(0)}}}},
		{ID: basicFirstScalarOldReal, Kind: stmt.Write, SQL: `DELETE FROM catalog_info_scalar_old WHERE change_id=? AND field=? AND kind=? AND old_real=? AND old_integer IS NULL AND old_text IS NULL RETURNING change_id,field,kind,old_integer,old_real,old_text`, Params: append(append([]stmt.Param{}, p...), stmt.Param{Name: "old_real", Type: stmt.Real}), Cohorts: []stmt.Cohort{{Name: "exact_real", Bindings: []any{int64(51), int64(9), int64(2), float64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_first_scalar_old.go"}
		defs[n].Plan = stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_scalar_old"}, Seeks: []string{"change_id=?"}}
	}
	return defs
}
