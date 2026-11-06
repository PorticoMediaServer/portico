package plan

import "testing"

func TestAutoindexNativeMetadataFailClosed(t *testing.T) {
	for _, origin := range []string{"pk", "u"} {
		if got, err := autoindexOrigin([]any{int64(0), "sqlite_autoindex_t_1", int64(1), origin, int64(0)}); err != nil || got != origin {
			t.Fatal(got, err)
		}
	}
	for _, row := range [][]any{
		nil, {int64(0), "x", int64(1), "c", int64(0)},
		{int64(0), "x", int64(0), "u", int64(0)},
		{int64(0), "x", int64(1), "u", int64(1)},
		{int64(0), "x", "1", "pk", int64(0)},
	} {
		if _, err := autoindexOrigin(row); err == nil {
			t.Fatal("non-constraint native metadata accepted", row)
		}
	}
	table := Rows{Values: [][]any{{int64(0), "id", "TEXT", int64(1), nil, int64(1), int64(0)}}}
	valid := []any{int64(0), int64(0), "id", int64(0), "BINARY", int64(1)}
	info := Rows{Values: [][]any{valid, {int64(1), int64(-1), nil, int64(0), "BINARY", int64(0)}}}
	if cols, err := autoindexColumns(info, table); err != nil || len(cols) != 1 {
		t.Fatal("auxiliary rowid was treated as a key column", cols, err)
	}
	for _, mutate := range []func([]any){
		func(r []any) { r[1] = int64(-2) }, func(r []any) { r[1] = int64(8) }, func(r []any) { r[2] = "other" },
		func(r []any) { r[0] = int64(1) }, func(r []any) { r[3] = int64(2) }, func(r []any) { r[4] = nil }, func(r []any) { r[5] = int64(2) },
	} {
		row := append([]any(nil), valid...)
		mutate(row)
		if _, err := autoindexColumns(Rows{Values: [][]any{row}}, table); err == nil {
			t.Fatal("false key accepted", row)
		}
	}
}
