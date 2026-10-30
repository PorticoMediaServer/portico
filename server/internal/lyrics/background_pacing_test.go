package lyrics

import (
	"context"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestBulkLyricWorkerCompletesWithForegroundPressure(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing/pressure/scale or measured slow test in short mode")
	}

	c, bulk, _ := bulkFixture(t)
	bulk.Batch = 1
	bulk.provider = func(context.Context, string) ([]acquired, error) { return nil, nil }
	tx, err := c.DB.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err = bulk.QueueTx(context.Background(), tx, "paced", "library"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	dbwork.RegisterForegroundProbe(t.Name(), func() bool { return true })
	defer dbwork.RegisterForegroundProbe(t.Name(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	exited := make(chan struct{})
	go func() { defer close(exited); bulk.Run(ctx) }()
	defer func() { cancel(); <-exited }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("foreground pressure prevented lyric run completing")
		case <-tick.C:
			run, err := bulk.Observe(context.Background(), "paced")
			if err != nil {
				t.Fatal(err)
			}
			if run.State == "succeeded" {
				if run.Candidates != 2 || run.Processed != 2 || run.Missing != 2 {
					t.Fatalf("pacing lost work: %+v", run)
				}
				return
			}
		}
	}
}
