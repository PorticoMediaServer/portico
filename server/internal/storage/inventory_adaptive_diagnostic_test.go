package storage

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
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

func TestRetainedRealDirectoryAdaptiveBudgetDiagnostic(t *testing.T) {
	for _, limit := range []int{16, 8} {
		t.Run(strconv.Itoa(limit), func(t *testing.T) {
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			c := New(binary)
			c.SourceOperations = NewOperationScope()
			t.Cleanup(func() {
				c.SourceOperations.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				if err := c.SourceOperations.Wait(ctx); err != nil {
					t.Error(err)
				}
			})
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "one.pdf"), []byte("unchanged"), 0600); err != nil {
				t.Fatal(err)
			}
			r := InventoryRequest{Root: root, RelativePath: ".", Limit: 16}
			first := awaitStreamPage(t, c, "adaptive-real", r)
			if first.Complete || len(first.Entries) != 1 {
				t.Fatal("control did not reach the real one-file / later-EOF boundary", first)
			}
			r.Cursor, r.RootIdentity, r.DirectoryRevision = first.NextCursor, first.RootIdentity, first.DirectoryRevision
			r.Limit = limit
			last := awaitStreamPage(t, c, "adaptive-real", r)
			if !last.Complete || len(last.Entries) != 0 {
				t.Fatal("unchanged directory did not reach actual EOF", last)
			}
		})
	}
}
