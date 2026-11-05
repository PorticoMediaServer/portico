package plan

import (
	"fmt"
	"slices"
	"strings"
)

type conflictColumn struct{ name, collation string }
type conflictTarget struct {
	index         string
	nativePrimary bool
	columns       []conflictColumn
}

// The grammar has no conflict COLLATE or WHERE clause. Thus every full UNIQUE
// index/PK on exactly this column set is a possible SQLite target, regardless
// of declaration order or index collation. A partial index cannot match. Reject
// multiple candidates rather than certifying whichever index the caller names.
func resolveConflict(a *InsertAudit, table *dmlTable, shape *dmlShape) (conflictTarget, error) {
	wanted := make([]string, len(shape.conflict))
	for i, c := range shape.conflict {
		wanted[i] = nameKey(c)
	}
	slices.Sort(wanted)
	var candidates []conflictTarget
	hasPKIndex := false
	for _, index := range a.Indexes.Values {
		if index[3] == "pk" {
			hasPKIndex = true
		}
		if index[2] != int64(1) || index[4] != int64(0) {
			continue
		}
		candidate := conflictTarget{index: index[1].(string), nativePrimary: table.withoutRowid && index[3] == "pk"}
		var columns []string
		plain := true
		for _, col := range a.IndexColumns[candidate.index].Values {
			if col[5] != int64(1) {
				continue
			}
			name, ok := col[2].(string)
			collation, collationOK := col[4].(string)
			if !ok || !collationOK || col[1].(int64) < 0 {
				plain = false
				break
			}
			name = foldName(name)
			columns = append(columns, name)
			candidate.columns = append(candidate.columns, conflictColumn{name, strings.ToUpper(collation)})
		}
		slices.Sort(columns)
		if plain && slices.Equal(columns, wanted) {
			candidates = append(candidates, candidate)
		}
	}
	if !table.withoutRowid && !hasPKIndex {
		var pk []string
		integer := false
		for _, col := range table.columns.Values {
			if col[5].(int64) > 0 {
				pk = append(pk, foldName(col[1].(string)))
				integer = strings.EqualFold(col[2].(string), "INTEGER")
			}
		}
		if len(pk) == 1 && integer && slices.Equal(pk, wanted) {
			candidates = append(candidates, conflictTarget{index: "<integer-primary-key>", nativePrimary: true, columns: []conflictColumn{{pk[0], "BINARY"}}})
		}
	}
	if len(candidates) != 1 {
		var names []string
		for _, c := range candidates {
			names = append(names, c.index+fmt.Sprint(c.columns))
		}
		return conflictTarget{}, fmt.Errorf("conflict target requires exactly one full UNIQUE index/PK; matches=%d %v", len(candidates), names)
	}
	return candidates[0], nil
}
