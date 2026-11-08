package read

import (
	"math"
	"reflect"
	"testing"
)

func TestRBGroupAuthorOrderDraft(t *testing.T) {
	zero, two := 0.0, 2.0
	rows := []rbAuthorOrderRow{
		{Book: 9, Part: 90, TitleKey: "A", Disc: 1},
		{Book: 4, Part: 40, Series: "Saga", Position: &two, TitleKey: "b", Disc: 0},
		{Book: 3, Part: 32, Series: "Saga", Position: &two, TitleKey: "B", Disc: 2},
		{Book: 3, Part: 31, Series: "saga", Position: &two, TitleKey: "b", Disc: 1},
		{Book: 2, Part: 20, Series: "SAGA", Position: &zero, TitleKey: "Z", Disc: 1},
		{Book: 1, Part: 10, Series: "Saga", TitleKey: "Z", Disc: 1},
	}
	for _, desc := range []bool{false, true} {
		got, e := rbAuthorOrderDraft(rows, desc)
		if e != nil {
			t.Fatal(e)
		}
		want := []int64{10, 20, 31, 32, 40, 90}
		if desc {
			want = []int64{90, 40, 32, 31, 20, 10}
		}
		var ids []int64
		for _, r := range got {
			ids = append(ids, r.Part)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Fatal(ids, want)
		}
	}
	got, _ := rbAuthorOrderDraft(rows, false)
	zero = 99
	if *got[1].Position != 0 {
		t.Fatal("mutable retained numeric key")
	}
	if _, e := rbAuthorOrderDraft(make([]rbAuthorOrderRow, MaxRaw+1), false); e != ErrNotReady {
		t.Fatal("unbounded fragment", e)
	}
}

func TestRBGroupAuthorRefusesNonfinitePositions(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		rows, e := rbAuthorOrderDraft([]rbAuthorOrderRow{{Book: 1, Part: 2, Position: &value}}, false)
		if e != ErrUnqualified || rows != nil {
			t.Fatal("nonfinite position established an order", rows, e)
		}
	}
}
