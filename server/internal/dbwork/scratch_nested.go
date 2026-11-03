package dbwork

import (
	"bytes"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

// Scratch branch only: detect a goroutine that holds an open transaction on one
// connection and runs a statement on another.
var scratchNested = struct {
	sync.Mutex
	owner  map[*observedConn]uint64
	stacks map[string]int
}{owner: map[*observedConn]uint64{}, stacks: map[string]int{}}

func scratchGoid() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	fields := bytes.Fields(buf[:n])
	if len(fields) < 2 {
		return 0
	}
	id, _ := strconv.ParseUint(string(fields[1]), 10, 64)
	return id
}

func scratchTxBegan(c *observedConn) {
	g := scratchGoid()
	scratchNested.Lock()
	scratchNested.owner[c] = g
	scratchNested.Unlock()
}

func scratchTxEnded(c *observedConn) {
	scratchNested.Lock()
	delete(scratchNested.owner, c)
	scratchNested.Unlock()
}

func scratchStatement(c *observedConn) {
	g := scratchGoid()
	scratchNested.Lock()
	nested := false
	for conn, owner := range scratchNested.owner {
		if owner == g && conn != c {
			nested = true
			break
		}
	}
	scratchNested.Unlock()
	if !nested {
		return
	}
	buf := make([]byte, 16<<10)
	buf = buf[:runtime.Stack(buf, false)]
	lines := []string{}
	for _, l := range strings.Split(string(buf), "\n") {
		if strings.Contains(l, "PorticoMediaServer") && !strings.Contains(l, "/dbwork.") && !strings.HasPrefix(strings.TrimSpace(l), "/") {
			if i := strings.Index(l, "("); i > 0 {
				l = l[:i]
			}
			lines = append(lines, strings.TrimSpace(l))
		}
	}
	if len(lines) > 12 {
		lines = lines[:12]
	}
	key := strings.Join(lines, " < ")
	scratchNested.Lock()
	scratchNested.stacks[key]++
	scratchNested.Unlock()
}

func ScratchNestedStacks() map[string]int {
	scratchNested.Lock()
	defer scratchNested.Unlock()
	out := map[string]int{}
	for k, v := range scratchNested.stacks {
		out[k] = v
	}
	return out
}
