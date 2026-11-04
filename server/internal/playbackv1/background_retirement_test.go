package playbackv1

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type retirementRollbackState struct {
	started, release, finished chan struct{}
	once                       sync.Once
	shutdownReturned           atomic.Bool
	late                       atomic.Int64
}
type retirementConnector struct {
	driver.Connector
	state *retirementRollbackState
}

func (c retirementConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &retirementConn{conn, c.state}, nil
}

type retirementConn struct {
	driver.Conn
	state *retirementRollbackState
}

func (c *retirementConn) BeginTx(ctx context.Context, opt driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opt)
	if err != nil {
		return nil, err
	}
	return &retirementTx{tx, c.state}, nil
}

type retirementTx struct {
	driver.Tx
	state *retirementRollbackState
}

func (tx *retirementTx) Rollback() error {
	tx.state.once.Do(func() { close(tx.state.started) })
	<-tx.state.release
	if tx.state.shutdownReturned.Load() {
		tx.state.late.Add(1)
	}
	err := tx.Tx.Rollback()
	close(tx.state.finished)
	return err
}
func TestPlaybackShutdownJoinsCancellationOwnedPhysicalRollback(t *testing.T) {
	base, err := sqlite.NewConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	state := &retirementRollbackState{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	db := sql.OpenDB(retirementConnector{base, state})
	defer db.Close()
	s := New(db, nil, nil)
	opened := make(chan error, 1)
	workerEnded := make(chan struct{})
	var release sync.Once
	unblock := func() { release.Do(func() { close(state.release) }) }
	defer unblock()
	s.startWorker("test.retirement-read-transaction", func(ctx context.Context) {
		defer close(workerEnded)
		_, finish, err := dbwork.BeginRead(ctx, db)
		opened <- err
		if err != nil {
			return
		}
		<-ctx.Done()
		<-state.started
		// The actual queue builder uses this BeginRead close function too. A
		// cancellation-owned SQL rollback already claimed tx.done, so this returns.
		finish()
	})
	if err = <-opened; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = s.Shutdown(ctx)
	state.shutdownReturned.Store(true)
	select {
	case <-workerEnded:
	default:
		t.Log("worker remains after incomplete shutdown")
	}
	select {
	case <-state.finished:
	default:
		if err == nil {
			t.Error("Shutdown returned nil while database/sql cancellation rollback was still running")
		}
	}
	unblock()
	<-state.finished
	t.Logf("shutdown error=%v; physical SQLite rollbacks after return=%d", err, state.late.Load())
	if err == nil && state.late.Load() != 0 {
		t.Error("successful Shutdown allowed a real SQLite rollback after return")
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
}
func TestPlaybackShutdownJoinsPrestartedAdmission(t *testing.T) {
	s := &Service{}
	life, finish, ok := s.beginBackground()
	if !ok {
		t.Fatal("initial admission refused")
	}
	result := make(chan error, 1)
	go func() { result <- s.Shutdown(context.Background()) }()
	select {
	case <-life.Done():
	case <-time.After(time.Second):
		finish()
		t.Fatal("shutdown did not cancel admitted work")
	}
	if s.startWorker("test.retirement-too-late", func(context.Context) { t.Error("late worker ran") }) {
		t.Error("late worker admitted")
	}
	select {
	case err := <-result:
		finish()
		t.Fatalf("join omitted admitted but not started worker: %v", err)
	default:
	}
	finish()
	if err := <-result; err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal("repeated shutdown", err)
	}
}
func TestPlaybackCaptureRetirementSharesCompletionAndDeadline(t *testing.T) {
	base, err := sqlite.NewConnector(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	state := &retirementRollbackState{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	db := sql.OpenDB(retirementConnector{base, state})
	defer db.Close()
	s := New(db, nil, nil)
	capture, err := s.openCapture(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	s.build.captures = map[string]*queryCapture{"retained": capture}
	var once sync.Once
	unblock := func() { once.Do(func() { close(state.release) }) }
	defer unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- s.Shutdown(ctx) }()
	<-state.started
	<-ctx.Done()
	firstReturned := false
	select {
	case err = <-result:
		firstReturned = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("deadline result: %v", err)
		}
		t.Log("deadline returned", err)
	case <-time.After(100 * time.Millisecond):
		t.Error("Shutdown ignored its deadline inside retained capture.close")
	}
	// A concurrent second call must not claim the same retained capture has joined.
	second, stopSecond := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer stopSecond()
	secondErr := s.Shutdown(second)
	if secondErr == nil {
		t.Error("second Shutdown claimed success while the first still owns a live capture rollback")
	}
	unblock()
	<-state.finished
	if !firstReturned {
		select {
		case <-result:
		case <-time.After(time.Second):
			t.Fatal("shutdown did not finish after release")
		}
	}
	if err = s.Shutdown(context.Background()); err != nil {
		t.Fatal("final physical join", err)
	}
}

// The queue's transient snapshots and ordinary maintenance writes share the
// same cancellation-owned rollback seam as BeginRead.
func TestPlaybackShutdownJoinsSnapshotAndWriteRetirement(t *testing.T) {
	for _, kind := range []string{"snapshot", "write"} {
		t.Run(kind, func(t *testing.T) {
			base, err := sqlite.NewConnector(":memory:")
			if err != nil {
				t.Fatal(err)
			}
			state := &retirementRollbackState{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
			db := sql.OpenDB(retirementConnector{base, state})
			defer db.Close()
			service := New(db, nil, nil)
			opened := make(chan error, 1)
			var release sync.Once
			unblock := func() { release.Do(func() { close(state.release) }) }
			defer unblock()
			service.startWorker("test.transaction-retirement", func(ctx context.Context) {
				if kind == "snapshot" {
					dbwork.WithReadSnapshot(ctx, db, func(ctx context.Context) error { opened <- nil; <-ctx.Done(); <-state.started; return ctx.Err() })
				} else {
					w, err := dbwork.Begin(ctx, db, dbwork.ClassMaintenance)
					opened <- err
					if err != nil {
						return
					}
					defer w.Rollback()
					<-ctx.Done()
					<-state.started
				}
			})
			if err = <-opened; err != nil {
				t.Fatal(err)
			}
			stop, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
			defer cancel()
			if err = service.Shutdown(stop); !errors.Is(err, context.DeadlineExceeded) {
				t.Errorf("physical rollback not joined: %v", err)
			}
			unblock()
			<-state.finished
			if err = service.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}
