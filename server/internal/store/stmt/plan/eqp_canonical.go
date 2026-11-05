package plan

import (
	"fmt"
	"slices"
)

// Only SQLite's notused field is physical metadata. Preserve native node IDs,
// parent links, detail text and column names exactly; never normalize findings.
func canonicalEQP(eqp Rows) (Rows, error) {
	if !slices.Equal(eqp.Columns, []string{"id", "parent", "notused", "detail"}) {
		return Rows{}, fmt.Errorf("plan: unexpected EQP columns")
	}
	result := Rows{Columns: slices.Clone(eqp.Columns), Values: make([][]any, len(eqp.Values))}
	for i, row := range eqp.Values {
		if len(row) != 4 {
			return Rows{}, fmt.Errorf("plan: invalid EQP row %d", i)
		}
		if _, ok := row[2].(int64); !ok {
			return Rows{}, fmt.Errorf("plan: invalid EQP notused at row %d", i)
		}
		result.Values[i] = slices.Clone(row)
		result.Values[i][2] = "<notused>"
	}
	return result, nil
}
