package storage

import (
	"strconv"
	"testing"
)

func TestRetainedProducerAdaptiveBudgetDiagnostic(t *testing.T) {
	for _, limit := range []int{16, 8} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			c, root := streamTestClient(t, "printf '%s\\n' '"+streamJSON(false)+"' '"+streamJSON(true)+"'\n")
			r := InventoryRequest{Root: root, RelativePath: ".", Limit: 16}
			first := awaitStreamPage(t, c, "adaptive", r)
			r.Cursor, r.RootIdentity, r.DirectoryRevision = first.NextCursor, first.RootIdentity, first.DirectoryRevision
			r.Limit = limit
			last := awaitStreamPage(t, c, "adaptive", r)
			if !last.Complete {
				t.Fatal("same directory did not reach its actual EOF")
			}
		})
	}
}
