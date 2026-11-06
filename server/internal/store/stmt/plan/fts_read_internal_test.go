package plan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

func TestFTSReadVOpenMappingIsExact(t *testing.T) {
	d := stmt.FTSRead{Slot: stmt.SearchFragmentLexicalReadV1, Shape: stmt.FTSFirst}
	audit := &FTSReadAudit{Descriptor: d, Identity: `"main"."search_fragment_lexical":fts5`}
	vm := Rows{Columns: []string{"addr", "opcode", "p1", "p2", "p3", "p4", "p5", "comment"}, Values: [][]any{
		{int64(5), "VOpen", int64(0), int64(0), int64(0), "vtab:ABC123", int64(0), nil},
		{int64(9), "VFilter", int64(0), int64(14), int64(2), "M1", int64(0), nil},
	}}
	got, err := canonicalFTSRead(vm, d, audit)
	if err != nil {
		t.Fatal(err)
	}
	if vm.Values[0][5] != "vtab:ABC123" {
		t.Fatal("raw evidence mutated")
	}
	for i, row := range vm.Values {
		for j, value := range row {
			want := value
			if i == 0 && j == 5 {
				want = audit.Identity
			}
			if !reflect.DeepEqual(got.Values[i][j], want) {
				t.Fatal("operand changed outside VOpen P4", i, j)
			}
		}
	}
	mismatch := Rows{Columns: vm.Columns, Values: append(append([][]any{}, vm.Values...), []any{int64(10), "VOpen", int64(1), int64(0), int64(0), "vtab:DEF456", int64(0), nil})}
	if _, err := canonicalFTSRead(mismatch, d, audit); err == nil || !strings.Contains(err.Error(), "pointers differ") {
		t.Fatal("mismatched VOpen pair", err)
	}
	outside := Rows{Columns: vm.Columns, Values: [][]any{{int64(1), "Noop", int64(0), int64(0), int64(0), "vtab:ABC123", int64(0), nil}}}
	if _, err := canonicalVM(outside, nil); err == nil || !strings.Contains(err.Error(), "fixed-slot") {
		t.Fatal("vtab operand outside approved slots", err)
	}
	if _, err := canonicalFTSRead(outside, d, audit); err == nil || !strings.Contains(err.Error(), "outside audited") {
		t.Fatal("vtab operand outside VOpen P4", err)
	}
	filter := Rows{Columns: vm.Columns, Values: [][]any{vm.Values[0], append([]any{}, vm.Values[1]...)}}
	filter.Values[1][5] = "M1>"
	if _, err := canonicalFTSRead(filter, d, audit); err == nil || !strings.Contains(err.Error(), "selector changed") {
		t.Fatal("VFilter selector was not kept exact", err)
	}
	wrong := *audit
	wrong.Identity = `"main"."unknown":fts5`
	if _, err := canonicalFTSRead(vm, d, &wrong); err == nil {
		t.Fatal("unknown virtual identity")
	}
}
