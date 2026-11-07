package read

import (
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
)

// These compiled pairs only choose QueryExactBytes for an already registered
// payload. They add no native SQL permission: both definitions still require an
// actual Capture receipt, with octet shapes owned by plan's domain registry.
type rsBytePoint struct {
	lengths string
	columns []int
}

var rsBytePoints = map[string]rsBytePoint{}

func registerRSBytePoint(payload string, p rsBytePoint) {
	if payload == "" || p.lengths == "" || len(p.columns) == 0 {
		panic("incomplete RS exact-byte point")
	}
	if _, ok := rsBytePoints[payload]; ok {
		panic("duplicate RS exact-byte point")
	}
	p.columns = append([]int(nil), p.columns...)
	seen := map[int]bool{}
	for _, c := range p.columns {
		if c < 0 || c >= 128 || seen[c] {
			panic("invalid RS byte projection")
		}
		seen[c] = true
	}
	rsBytePoints[payload] = p
}
func queryReadScope(scope *governor.ReadScope, name string, args ...any) ([][]any, int, error) {
	p, ok := rsBytePoints[name]
	if !ok {
		rows, err := scope.Query(readStatementID(name), args...)
		return rows, 1, err
	}
	row, err := scope.QueryExactBytes(readStatementID(p.lengths), readStatementID(name), p.columns, args...)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 1, nil
	}
	if err != nil {
		return nil, 2, err
	} // Pair attempts; an early error may stop before the payload.
	return [][]any{row}, 2, nil
}
