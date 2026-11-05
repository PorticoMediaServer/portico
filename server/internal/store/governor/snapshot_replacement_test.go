package governor

import (
	"context"
	"database/sql/driver"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

type snapshotObservedConnector struct {
	driver.Connector
	observe func()
}

func (c *snapshotObservedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	c.observe()
	conn, err := c.Connector.Connect(ctx)
	c.observe()
	return conn, err
}

func TestSnapshotReplacementKeepsReservationThroughClose(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, _ := snapshotGovernor(t)
	cfgGov := g.(*ledger).config
	cfgGov.Partitions[Foreground][Reader] = 2 // Enough for the formerly queued second grant.
	g, a, err := New(cfgGov)
	if err != nil {
		t.Fatal(err)
	}
	cfg := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal, ReadLimits: sqliteconn.ReadLimits{LengthBytes: 16384, Columns: 16}}
	connector, err := sqliteconn.Connector(path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &snapshotConnector{Connector: connector, closeEntered: make(chan struct{}), releaseClose: make(chan struct{})}
	var unreserved atomic.Int64
	observed := &snapshotObservedConnector{Connector: c, observe: func() {
		if a.Usage()[Foreground][MemoryBytes] < snapshotRequest().Need[MemoryBytes] {
			unreserved.Add(1)
		}
	}}
	s, err := NewSnapshots(observed, cfg, g, a, snapshotRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	released := false
	defer func() {
		if !released {
			close(c.releaseClose)
		}
	}()
	c.failRollback.Store(true)
	first := make(chan error, 1)
	go func() { _, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); first <- err }()
	select {
	case <-c.closeEntered:
	case <-time.After(time.Second):
		t.Fatal("physical close not reached")
	}
	held := len(s.slots) == 1 && a.Usage()[Foreground][MemoryBytes] == snapshotRequest().Need[MemoryBytes]
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	second := make(chan error, 1)
	go func() { _, err := s.Read(ctx, snapshotRequest(), time.Second, snapshotQueryOne); second <- err }()
	var secondErr error
	refused := false
	select {
	case secondErr = <-second:
		refused = true
	case <-time.After(100 * time.Millisecond):
	}
	cancel()
	close(c.releaseClose)
	released = true
	if !refused {
		secondErr = <-second
	}
	if err := <-first; !errors.Is(err, snapshotRollbackError) {
		t.Fatal(err)
	}
	if !held || !refused || !errors.Is(secondErr, ErrBusy) {
		t.Fatal("read queued behind owned cleanup", held, refused, secondErr)
	}
	if c.opened.Load() != 1 || s.db.Stats().WaitCount != 0 {
		t.Fatal("background replacement was scheduled", c.opened.Load(), s.db.Stats())
	}
	c.failRollback.Store(false)
	if _, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); err != nil {
		t.Fatal("replacement failed", err)
	}
	if unreserved.Load() != 0 || c.opened.Load() != 2 || a.Usage() != [4]Amounts{} || len(s.slots) != 0 {
		t.Fatal("replacement lost custody", unreserved.Load(), c.opened.Load(), a.Usage(), len(s.slots))
	}
}
