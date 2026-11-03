package dbwork

import (
	"strings"
	"sync"
)

// Scratch branch only: which statements flush a connection's statement cache.
var scratchFlushes = struct {
	sync.Mutex
	byQuery map[string][2]int
}{byQuery: map[string][2]int{}}

func ScratchNoteFlush(query string, entries int) {
	shape := strings.Join(strings.Fields(query), " ")
	if len(shape) > 400 {
		shape = shape[:400]
	}
	scratchFlushes.Lock()
	v := scratchFlushes.byQuery[shape]
	scratchFlushes.byQuery[shape] = [2]int{v[0] + 1, v[1] + entries}
	scratchFlushes.Unlock()
}

func ScratchFlushes() map[string][2]int {
	scratchFlushes.Lock()
	defer scratchFlushes.Unlock()
	out := map[string][2]int{}
	for k, v := range scratchFlushes.byQuery {
		out[k] = v
	}
	return out
}

func ScratchResetFlushes() {
	scratchFlushes.Lock()
	scratchFlushes.byQuery = map[string][2]int{}
	scratchFlushes.Unlock()
}
