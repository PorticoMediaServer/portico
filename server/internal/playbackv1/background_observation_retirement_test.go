package playbackv1

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
	"strings"
	"sync"
	"testing"
	"time"
)

type observationRetirementConnector struct {
	driver.Connector
	state *retirementRollbackState
}

func (c observationRetirementConnector) Connect(ctx context.Context) (driver.Conn, error) {
	conn, err := c.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	return &observationRetirementConn{Conn: conn, state: c.state}, nil
}

type observationRetirementConn struct {
	driver.Conn
	state *retirementRollbackState
}

func (c *observationRetirementConn) BeginTx(ctx context.Context, opt driver.TxOptions) (driver.Tx, error) {
	tx, err := c.Conn.(driver.ConnBeginTx).BeginTx(ctx, opt)
	if err != nil {
		return nil, err
	}
	return &retirementTx{Tx: tx, state: c.state}, nil
}
func (c *observationRetirementConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	if strings.HasPrefix(query, "SAVEPOINT observation") {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if pc, ok := c.Conn.(driver.ConnPrepareContext); ok {
		return pc.PrepareContext(ctx, query)
	}
	return c.Conn.Prepare(query)
}
func TestPlaybackObservationPhysicalRetirementHonorsShutdownDeadline(t *testing.T) {
	f := newObservationFixture(t)
	epoch := f.claim(f.session.ID, "")
	if _, err := f.tick(f.session.ID, epoch, 1, "playing"); err != nil {
		t.Fatal(err)
	}
	o := f.service.observations()
	o.mu.Lock()
	cancelWorker, workerDone := o.workerCancel, o.workerDone
	o.mu.Unlock()
	if cancelWorker != nil {
		cancelWorker()
	}
	if workerDone != nil {
		<-workerDone
	}
	o.mu.Lock()
	entry := o.entries[f.session.ID]
	if entry == nil {
		o.mu.Unlock()
		t.Fatal("missing real observer")
	}
	entry.dirty = true
	o.mu.Unlock()
	original := f.service.DB
	base, err := sqlite.NewConnector(dbwork.DatabaseFile(context.Background(), original))
	if err != nil {
		t.Fatal(err)
	}
	state := &retirementRollbackState{started: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	wrapped := sql.OpenDB(observationRetirementConnector{base, state})
	defer wrapped.Close()
	f.service.DB = wrapped
	defer func() { f.service.DB = original }()
	var once sync.Once
	unblock := func() { once.Do(func() { close(state.release) }) }
	defer unblock()
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- f.service.Shutdown(ctx) }()
	select {
	case <-state.started:
	case <-time.After(5 * time.Second):
		t.Fatal("flush did not reach held physical rollback")
	}
	<-ctx.Done()
	returned := false
	select {
	case err = <-result:
		returned = true
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Error("deadline result", err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("Shutdown exceeded its deadline inside synchronous CloseObservations -> FlushObservations -> physical rollback")
	}
	second, stopSecond := context.WithTimeout(context.Background(), 25*time.Millisecond)
	if secondErr := f.service.Shutdown(second); !errors.Is(secondErr, context.DeadlineExceeded) {
		t.Errorf("concurrent shutdown bypassed physical flush retirement: %v", secondErr)
	}
	stopSecond()
	unblock()
	<-state.finished
	if !returned {
		select {
		case err = <-result:
			t.Log("shutdown after physical release", err)
		case <-time.After(time.Second):
			t.Fatal("shutdown still blocked after rollback release")
		}
	}
	o.mu.Lock()
	o.entries = map[string]*observer{}
	o.mu.Unlock()
	// Join the failed attempt before retrying its final flush. A caller already
	// joining it must receive that attempt's error, rather than another caller
	// replacing its completion while physical cleanup is still running.
	join, stopJoin := context.WithTimeout(context.Background(), time.Second)
	defer stopJoin()
	if err = f.service.Shutdown(join); err != nil && !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("failed-attempt join", err)
	}
	if err = f.service.Shutdown(join); err != nil {
		t.Fatal("retry after physical join", err)
	}
}
