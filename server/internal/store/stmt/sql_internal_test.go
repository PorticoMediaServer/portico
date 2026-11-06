package stmt

import (
	"reflect"
	"testing"
)

func TestParameterLayoutLimitSlots(t *testing.T) {
	for _, c := range []struct {
		sql    string
		count  int
		limits []int
	}{
		{"SELECT ? WHERE x=? LIMIT ?", 3, []int{2}},
		{"SELECT 'LIMIT ?',\"LIMIT\",`?`,[LIMIT] WHERE x=? LIMIT /* ? */ ?", 2, []int{1}},
		{"SELECT ? -- LIMIT ?\nWHERE x=? lImIt\f?", 3, []int{2}},
		{"SELECT ? /* LIMIT ? */ WHERE x=? LIMIT 1", 2, nil},
		{"SELECT limit_value FROM t WHERE x=?", 1, nil},
	} {
		count, limits, err := parameterLayout(c.sql)
		if err != nil || count != c.count || !reflect.DeepEqual(limits, c.limits) {
			t.Fatal(c.sql, count, limits, err)
		}
	}
}
