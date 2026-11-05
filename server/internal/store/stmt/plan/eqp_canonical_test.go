package plan

import (
	"encoding/json"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestCanonicalEQPOnlyReplacesNotused(t *testing.T) {
	raw := Rows{Columns: []string{"id", "parent", "notused", "detail"}, Values: [][]any{
		{int64(3), int64(0), int64(46), "SEARCH t USING INDEX ix (id=?)"},
		{int64(7), int64(3), int64(39), "CORRELATED SCALAR SUBQUERY 1"},
	}}
	before, _ := json.Marshal(raw)
	canonical, err := canonicalEQP(raw)
	if err != nil {
		t.Fatal(err)
	}
	for i, row := range raw.Values {
		for j, value := range row {
			want := value
			if j == 2 {
				want = "<notused>"
			}
			if canonical.Values[i][j] != want {
				t.Fatalf("changed exact field row%d/cell%d", i, j)
			}
		}
	}
	after, _ := json.Marshal(raw)
	if string(before) != string(after) {
		t.Fatal("raw EQP mutated")
	}
	if err := CheckEQP(stmt.Definition{ID: "canonical.guard"}, canonical); err == nil {
		t.Fatal("canonicalization erased native finding")
	}
	canonical.Columns[0] = "changed"
	if raw.Columns[0] != "id" {
		t.Fatal("column alias escaped")
	}
	for _, bad := range []Rows{
		{},
		{Columns: raw.Columns, Values: [][]any{{int64(1)}}},
		{Columns: raw.Columns, Values: [][]any{{int64(1), int64(0), "unknown", "SEARCH t"}}},
	} {
		if _, err := canonicalEQP(bad); err == nil {
			t.Fatal("malformed/unknown EQP masked")
		}
	}
}
