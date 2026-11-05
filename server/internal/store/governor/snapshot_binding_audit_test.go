//go:build libc.memgrind

package governor

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"modernc.org/libc"
	"testing"
)

func TestSnapshotBoundFirstStepPanicNativeAudit(t *testing.T) {
	for _, kind := range []stmt.Type{stmt.Text, stmt.Blob} {
		t.Run(string(kind), func(t *testing.T) {
			attempt := snapshotBoundAttempts(t, kind)
			attempt(false)
			for _, panics := range []bool{false, true, false} {
				libc.MemAuditStart()
				attempt(panics)
				if err := libc.MemAuditReport(); err != nil {
					t.Fatal("native binding allocation audit", panics, err)
				}
			}
		})
	}
}
