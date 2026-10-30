package catalog

import (
	"context"
	"errors"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"testing"
	"time"
)

func TestCanceledCatalogReadReleasesPoolWait(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pool := dbwork.ReadHandle(ctx, db)
	pool.SetMaxOpenConns(1)
	held, err := pool.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	before := pool.Stats().WaitCount
	done := make(chan error, 1)
	go func() { _, err := New(db).WithContext(ctx).LibraryForItem("missing"); done <- err }()
	deadline := time.After(5 * time.Second)
	for pool.Stats().WaitCount == before {
		select {
		case <-deadline:
			t.Fatal("read did not wait on exhausted pool")
		case <-time.After(time.Millisecond):
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled read retained its pool wait")
	}
	held.Close()
	probe, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := pool.PingContext(probe); err != nil {
		t.Fatal("connection not reusable", err)
	}
}

func TestCanceledCatalogSnapshotReleasesConnection(t *testing.T) {
	db, err := persistence.Open(filepath.Join(t.TempDir(), "catalog.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	pool := dbwork.ReadHandle(context.Background(), db)
	pool.SetMaxOpenConns(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error {
			close(started)
			var value int
			return New(db).WithContext(ctx).read().QueryRow(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1000000000) SELECT sum(x) FROM n`).Scan(&value)
		})
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled query succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("snapshot did not release promptly")
	}
	probe, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if err := pool.PingContext(probe); err != nil {
		t.Fatal("snapshot retained connection", err)
	}
}
