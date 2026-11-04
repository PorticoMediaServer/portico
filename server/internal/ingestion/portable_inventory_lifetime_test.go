//go:build !linux

package ingestion

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

func TestPortableScanCancellationRetiresItsRegisteredProducer(t *testing.T) {
	for _, cancelJob := range []bool{false, true} {
		t.Run(fmt.Sprintf("job=%t", cancelJob), func(t *testing.T) {
			f := newLoopFixture(t, "book")
			for i := range 500 {
				f.write(fmt.Sprintf("book-%04d.pdf", i), "file-list-only source")
			}
			binary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			client := storage.New(binary)
			client.SourceOperations = storage.NewOperationScope()
			f.s.SetStorage(client)
			oldMonitor := monitorRun
			monitorRun = func(_ *Service, ctx context.Context) { <-ctx.Done() }
			t.Cleanup(func() { monitorRun = oldMonitor })
			entered, release := make(chan struct{}), make(chan struct{})
			var once, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			client.Guard = func(string) error {
				var count int
				if err := f.db.QueryRow(`SELECT count(*) FROM inventory_objects WHERE source_id=?`, f.source).Scan(&count); err != nil {
					return err
				}
				if count > 0 {
					once.Do(func() { close(entered); <-release })
					return context.Canceled
				}
				return nil
			}
			job := f.queue()
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() { defer close(done); f.s.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				unblock()
				waitRunSignal(t, done, "portable scan exit")
				client.SourceOperations.Close()
				joined, stop := context.WithTimeout(context.Background(), 3*time.Second)
				defer stop()
				if err := client.SourceOperations.Wait(joined); err != nil {
					t.Error("physical source cleanup", err)
				}
			})
			waitRunSignal(t, entered, "committed first page and held next turn")
			if client.SourceOperations.Active() == 0 {
				t.Fatal("fixture did not retain the original portable producer")
			}
			if cancelJob {
				if err := f.s.Cancel(job.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				cancel()
			}
			unblock()
			if !cancelJob {
				waitRunSignal(t, done, "Run's physical retirement")
			} else {
				deadline := time.Now().Add(3 * time.Second)
				for client.SourceOperations.Active() != 0 && time.Now().Before(deadline) {
					time.Sleep(time.Millisecond)
				}
			}
			if active := client.SourceOperations.Active(); active != 0 {
				t.Fatalf("abandoned scan retained %d physical producer slots", active)
			}
		})
	}
}
