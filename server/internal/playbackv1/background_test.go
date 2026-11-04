package playbackv1

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/catalogtest"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

func TestPlaybackShutdownJoinsTheLastDatabaseTouchAndRefusesNewWorkers(t *testing.T) {
	c := catalogtest.Open(t)
	s := New(c.DB, nil, nil)
	started, result, finished := make(chan error, 1), make(chan error, 1), make(chan struct{})
	release := make(chan struct{})
	s.startWorker("test.playback-database-lifetime", func(ctx context.Context) {
		defer close(finished)
		conn, err := c.DB.Conn(ctx)
		started <- err
		if err != nil {
			result <- err
			return
		}
		select {
		case <-ctx.Done():
		case <-release:
		}
		// Even cancellation cleanup's final query must finish before the join.
		var n int
		err = conn.QueryRowContext(context.Background(), "SELECT 1").Scan(&n)
		closeErr := conn.Close()
		result <- errors.Join(err, closeErr)
	})
	t.Cleanup(func() { close(release); s.Shutdown(context.Background()); <-finished })
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	join, cancelJoin := context.WithTimeout(t.Context(), time.Second)
	defer cancelJoin()
	if err := s.Shutdown(join); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
		if err := <-result; err != nil {
			t.Fatal(err)
		}
	default:
		t.Fatal("shutdown returned while a worker could still touch the database")
	}
	if s.startWorker("test.after-shutdown", func(context.Context) { t.Error("worker touched database after shutdown") }) {
		t.Fatal("shutdown admitted another database worker")
	}
	if err := c.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if !dbwork.ConnectionsDrained(c.DB) {
		t.Fatal("joined worker retained a SQLite connection")
	}
}

func TestPlaybackShutdownDeadlineBoundsAStuckWorkerAndCanJoinAgain(t *testing.T) {
	s := &Service{}
	started, release := make(chan struct{}), make(chan struct{})
	s.startWorker("test.stuck-playback-sweep", func(context.Context) { close(started); <-release })
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() {
		unblock()
		if err := s.Shutdown(context.Background()); err != nil {
			t.Error(err)
		}
	})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	returned := make(chan error, 1)
	go func() { returned <- s.Shutdown(ctx) }()
	select {
	case err := <-returned:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("stuck worker was reported joined: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("shutdown ignored its deadline")
	}
	if s.startWorker("test.after-timeout", func(context.Context) {}) {
		t.Fatal("timed-out shutdown reopened admission")
	}
	unblock()
	join, cancelJoin := context.WithTimeout(context.Background(), time.Second)
	defer cancelJoin()
	if err := s.Shutdown(join); err != nil {
		t.Fatal("second shutdown did not join the released worker", err)
	}
}
