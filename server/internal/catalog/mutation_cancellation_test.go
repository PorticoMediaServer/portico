package catalog

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// Lane deadlines must reach the write gate. A cancelled browser must leave no
// queued mutation that executes later when unrelated work releases the writer.
func TestCatalogMutationsAbandonContendedWriterAtRequestDeadline(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	for _, name := range []string{"create-collection", "record-search", "clear-search", "rename-collection", "delete-collection", "set-collection-item", "batch-collection-items"} {
		t.Run(name, func(t *testing.T) {
			s, _, v := savedFixture(t)
			release, err := dbwork.WriteGate().Acquire(context.Background(), dbwork.ClassInteractive)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			bound := s.WithContext(ctx)
			result := make(chan error, 1)
			go func() {
				var e error
				switch name {
				case "create-collection":
					_, e = bound.CreateCollection("library", "Cancelled")
				case "record-search":
					e = bound.RecordSearch(v, "cancelled search")
				case "clear-search":
					_, e = bound.ClearSearchHistory(v)
				case "rename-collection":
					_, e = bound.RenameCollection("unused", "Cancelled")
				case "delete-collection":
					e = bound.DeleteCollection("unused")
				case "set-collection-item":
					e = bound.SetCollectionItem("unused", "unused", true)
				case "batch-collection-items":
					_, e = bound.SetCollectionItems("unused", []string{"unused"}, nil)
				}
				result <- e
			}()
			select {
			case e := <-result:
				if !errors.Is(e, context.DeadlineExceeded) {
					t.Fatalf("mutation lost request deadline: %v", e)
				}
			case <-time.After(time.Second):
				release()
				<-result
				t.Fatal("mutation waited beyond its request deadline")
			}
		})
	}
}
