package plan

import "testing"

func TestNativeConflictRootCannotUseOtherCursorsOrSubprograms(t *testing.T) {
	open := func(db, flags int64) []any {
		return []any{int64(1), "OpenWrite", int64(3), int64(21), db, nil, flags, nil}
	}
	check := []any{int64(2), "NoConflict", int64(3), int64(8), int64(0), nil, int64(0), nil}
	for _, op := range []string{"NoConflict", "NotExists"} {
		row := append([]any(nil), check...)
		row[1] = op
		if !nativeConflictRoot(Rows{Values: [][]any{open(0, 0), row}}, 21) {
			t.Fatal("native key check missing")
		}
	}
	for _, rows := range [][][]any{
		{check},
		{open(1, 0), check},
		{open(0, 0x10), check},
		{open(0, 0), open(1, 0), check},
		{open(0, 0), {int64(2), "Close", int64(3), int64(0), int64(0), nil, int64(0), nil}, check},
		{open(0, 0), {int64(0), "Init", int64(0), int64(0), int64(0), nil, int64(0), nil}, check},
		{{int64(0), "OpenWrite"}},
		{{int64(1), "OpenWrite", int64(3), "21", int64(0), nil, int64(0), nil}, check},
	} {
		if nativeConflictRoot(Rows{Values: rows}, 21) {
			t.Fatalf("unrelated/dynamic/closed/subprogram cursor accepted: %v", rows)
		}
	}
	if nativeConflictRoot(Rows{Values: [][]any{check}}, 0) {
		t.Fatal("missing cursor resolved as root zero")
	}
}
