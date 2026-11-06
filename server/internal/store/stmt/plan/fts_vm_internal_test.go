package plan

import (
	"reflect"
	"testing"
)

func ftsVMFixture() Rows {
	i := func(v int) int64 { return int64(v) }
	return Rows{Columns: []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}, Values: [][]any{
		{i(0), "Init", i(0), i(8), i(0), nil, i(0), nil},
		{i(1), "Variable", i(2), i(3), i(0), nil, i(0), nil},
		{i(2), "Null", i(0), i(1), i(0), nil, i(0), nil},
		{i(3), "Variable", i(1), i(2), i(0), nil, i(0), nil},
		{i(4), "IsNull", i(2), i(6), i(0), nil, i(0), nil},
		{i(5), "MustBeInt", i(2), i(0), i(0), nil, i(0), nil},
		{i(6), "VUpdate", i(1), i(5), i(1), "vtab:123ABCD", i(2), nil},
		{i(7), "Halt", i(0), i(0), i(0), nil, i(0), nil},
		{i(8), "Transaction", i(0), i(1), i(1466), "0", i(1), nil},
		{i(9), "VBegin", i(0), i(0), i(0), "vtab:123ABCD", i(0), nil},
		{i(10), "Null", i(0), i(4), i(0), nil, i(0), nil},
		{i(11), "Null", i(0), i(5), i(0), nil, i(0), nil},
		{i(12), "Goto", i(0), i(1), i(0), nil, i(0), nil},
	}}
}

func TestFTSVMMapsOnlyTwoDestinationProvenOperands(t *testing.T) {
	identity := `"main"."search_fragment_prefix":fts5`
	before, input := ftsVMFixture(), ftsVMFixture()
	out, err := mapFTSInsertVM(input, identity)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input, before) {
		t.Fatal("mapping mutated raw native evidence")
	}
	before.Values[6][5], before.Values[9][5] = identity, identity
	if !reflect.DeepEqual(out, before) {
		t.Fatal("mapping changed an unapproved operand")
	}
	for name, mutate := range map[string]func(*Rows){
		"mismatched_pair": func(r *Rows) { r.Values[9][5] = "vtab:987654" },
		"third_vtab":      func(r *Rows) { r.Values[10][5] = "vtab:123ABCD" },
		"other_operand":   func(r *Rows) { r.Values[6][6] = int64(3) },
		"truncated_row":   func(r *Rows) { r.Values[6] = nil },
		"zero_pointer":    func(r *Rows) { r.Values[6][5], r.Values[9][5] = "vtab:00", "vtab:00" },
	} {
		t.Run(name, func(t *testing.T) {
			in := ftsVMFixture()
			mutate(&in)
			if _, err := mapFTSInsertVM(in, identity); err == nil {
				t.Fatal("unapproved VM difference was erased")
			}
		})
	}
	if _, err := mapFTSInsertVM(ftsVMFixture(), `"main"."unknown":fts5`); err == nil {
		t.Fatal("unknown virtual destination mapped")
	}
}
