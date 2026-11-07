package read

// This cache exists only within ONE physical ReadScope. A fresh final scope
// always queries its own kind facts. Returning a new row prevents validators
// mutating the cached row container; the bounded name string is immutable.
type nativeKindFact struct {
	id, container int64
	name          string
}

func (f nativeKindFact) rows() [][]any { return [][]any{{f.id, f.name, f.container}} }
func nativeCachedKind(cache map[int64]nativeKindFact, name string, args []any) (nativeKindFact, bool) {
	if name != "full.native.kind" || len(args) != 1 {
		return nativeKindFact{}, false
	}
	id, ok := args[0].(int64)
	if !ok {
		return nativeKindFact{}, false
	}
	f, ok := cache[id]
	return f, ok
}
func nativeRememberKind(cache map[int64]nativeKindFact, name string, rows [][]any) {
	if name != "full.native.kind" || len(rows) != 1 || len(rows[0]) != 3 {
		return
	}
	id, a := rows[0][0].(int64)
	text, b := rows[0][1].(string)
	container, c := rows[0][2].(int64)
	if !a || !b || !c || id < 1 || id > 99 || container < 0 || container > 1 {
		return
	}
	cache[id] = nativeKindFact{id: id, name: text, container: container}
}
