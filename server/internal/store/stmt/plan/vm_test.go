package plan

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestVMCanonicalOperandsAndExactRemainder(t *testing.T) {
	roots := map[rootKey]string{{0, 21}: `"main"."items"`, {1, 21}: `"temp"."items"`}
	op := func(name string, p1, p2, p3, p5 int64) []any {
		return []any{int64(7), name, p1, p2, p3, "exact P4", p5, "exact comment"}
	}
	for _, tc := range []struct {
		row  []any
		cell int
		want string
	}{
		{op("Transaction", 0, 0, 1421, 1), 4, "<schema-cookie>"},
		{op("OpenRead", 3, 21, 0, 2), 3, roots[rootKey{0, 21}]},
		{op("OpenWrite", 3, 21, 1, 8), 3, roots[rootKey{1, 21}]},
		{op("ReopenIdx", 3, 21, 0, 2), 3, roots[rootKey{0, 21}]},
		{op("Clear", 21, 1, 4, 0), 2, roots[rootKey{1, 21}]},
		{op("Destroy", 21, 4, 0, 0), 2, roots[rootKey{0, 21}]},
		{op("TableLock", 0, 21, 1, 0), 3, roots[rootKey{0, 21}]},
	} {
		t.Run(tc.row[1].(string), func(t *testing.T) {
			vm := Rows{Columns: []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}, Values: [][]any{tc.row}}
			before, _ := json.Marshal(vm)
			actual, err := canonicalVM(vm, roots)
			if err != nil {
				t.Fatal(err)
			}
			for j, value := range tc.row {
				want := value
				if j == tc.cell {
					want = tc.want
				}
				if !reflect.DeepEqual(actual.Values[0][j], want) {
					t.Fatalf("cell %d: want %#v, got %#v", j, want, actual.Values[0][j])
				}
			}
			after, _ := json.Marshal(vm)
			if string(before) != string(after) {
				t.Fatal("input VM mutated")
			}
		})
	}
	for _, row := range [][]any{
		op("OpenWrite", 3, 21, 0, 0x10),  // P2 is a register, not root 21.
		op("CreateBtree", 0, 21, 1, 0),   // P2 is an output register.
		op("Transaction", 0, 0, 1421, 0), // P3 is not checked.
		op("SeekGE", 3, 21, 4, 2),        // P2 is a jump target.
	} {
		vm := Rows{Columns: []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}, Values: [][]any{row}}
		actual, err := canonicalVM(vm, roots)
		if err != nil || !reflect.DeepEqual(actual, vm) {
			t.Fatalf("nonphysical operand changed: %+v, %v", actual, err)
		}
	}
}

func TestVMCanonicalRejectsUnresolvedOrMalformedRoots(t *testing.T) {
	columns := []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}
	for _, row := range [][]any{
		{int64(0), "OpenRead", int64(0), int64(99), int64(0), nil, int64(0), nil},
		{int64(0), "OpenRead", int64(0), int64(2), int64(9), nil, int64(0), nil},
		{int64(0), "OpenRead", int64(0), "2", int64(0), nil, int64(0), nil},
		{int64(0), "OpenWrite", int64(0), int64(2), int64(0), nil, "0", nil},
		{int64(0), "Transaction", int64(0), int64(0), "cookie", nil, int64(1), nil},
		{int64(0), "IntegrityCk", int64(0), int64(1), int64(0), "[2]", int64(0), nil},
		{int64(0), int64(3), int64(0), int64(0), int64(0), nil, int64(0), nil},
		{int64(0), "OpenRead"},
	} {
		if _, err := canonicalVM(Rows{Columns: columns, Values: [][]any{row}}, map[rootKey]string{{0, 2}: "items"}); err == nil {
			t.Fatalf("malformed/unmapped VM accepted: %v", row)
		}
	}
	if _, err := canonicalVM(Rows{}, nil); err == nil {
		t.Fatal("unknown EXPLAIN columns accepted")
	}
}
