//go:build memory.counters && !libc.memgrind

package governor

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"modernc.org/libc"
	"testing"
)

func TestSnapshotBoundFirstStepPanicNativeAllocations(t *testing.T) {
	for _, kind := range []stmt.Type{stmt.Text, stmt.Blob} {
		t.Run(string(kind), func(t *testing.T) {
			attempt := snapshotBoundAttempts(t, kind)
			attempt(false) // Warm the same real connector/query/cleanup path.
			for _, panics := range []bool{false, true, false} {
				before := libc.MemStat().Allocs
				if before <= 0 {
					t.Fatal("native allocator counters unavailable")
				}
				attempt(panics)
				after := libc.MemStat().Allocs
				t.Logf("panic=%v native live allocation delta=%d", panics, after-before)
				if after != before {
					t.Fatal("native STATIC binding allocation leaked", before, after)
				}
			}
		})
	}
}
