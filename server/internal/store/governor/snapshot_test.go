package governor

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
)

func snapshotFixture(t *testing.T) (string, *sql.Conn) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "governor.sqlite")
	c := sqliteconn.Config{Role: sqliteconn.Writer, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	db, settings, err := sqliteconn.Open(t.Context(), path, c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if settings.Engine != "3.53.4" || settings.TempStore != 1 {
		t.Fatalf("invalid control settings %+v", settings)
	}
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err = sqliteconn.Assert(t.Context(), conn, c); err != nil {
		t.Fatal(err)
	}
	// Tiny test-only source, not a change to INT's canonical DDL.
	if _, err = conn.ExecContext(t.Context(), "CREATE TABLE source(id INTEGER PRIMARY KEY,value TEXT); INSERT INTO source VALUES(1,'first')"); err != nil {
		t.Fatal(err)
	}
	return path, conn
}

func snapshotGovernor(t *testing.T) (Governor, *Authority) {
	t.Helper()
	c := Config{Now: time.Now, MaxGrantsPerClass: 8}
	for i := range c.Partitions {
		c.Partitions[i] = Amounts{Reader: 1, MemoryBytes: 4 << 20, CPUMilli: 1000, IOBytes: 4 << 20, WALBytes: 4 << 20}
	}
	g, a, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return g, a
}

func snapshotRequest() Request {
	return Request{Key: JobKey{Domain: 1, ID: "page"}, Owner: 1, Class: Foreground, Need: Amounts{Reader: 1, MemoryBytes: 64 << 10}, Deadline: time.Now().Add(2 * time.Second)}
}

type snapshotCountedConnector struct {
	driver.Connector
	opened atomic.Int64
}

func (c *snapshotCountedConnector) Connect(ctx context.Context) (driver.Conn, error) {
	x, err := c.Connector.Connect(ctx)
	if err == nil {
		c.opened.Add(1)
	}
	return x, err
}

func snapshotReaderDriver(t *testing.T, path string, g Governor, a *Authority) (*Snapshots, *sql.DB, *snapshotCountedConnector) {
	t.Helper()
	c := sqliteconn.Config{Role: sqliteconn.Reader, Connections: 1, CacheKiB: 1024, Synchronous: sqliteconn.Normal}
	connector, err := sqliteconn.Connector(path, c)
	if err != nil {
		t.Fatal(err)
	}
	counted := &snapshotCountedConnector{Connector: connector}
	db := sql.OpenDB(counted)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s, err := NewSnapshots(db, c, g, a)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close(); db.Close() })
	return s, db, counted
}

func snapshotQueryOne(s *ReadScope) error {
	r, err := s.Query("SELECT id FROM source ORDER BY id LIMIT 1")
	if err != nil {
		return err
	}
	defer r.Close()
	if !r.Next() {
		return errors.Join(errors.New("missing row"), r.Err())
	}
	var id int
	return r.Scan(&id)
}

func TestSnapshotClosesRetainedScopeRowsBetweenWindows(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, _ := snapshotReaderDriver(t, path, g, a)
	var scope *ReadScope
	var rows *SnapshotRows
	out, err := s.Read(t.Context(), snapshotRequest(), 10*time.Second, func(r *ReadScope) error {
		scope = r
		var e error
		rows, e = r.Query("SELECT id FROM source")
		return e
	})
	if err != nil || out.Refusal != nil {
		t.Fatal(out, err)
	}
	if out.Settings.QueryOnly != 1 || out.Settings.TempStore != 1 || out.Settings.AutoCheckpoint != 0 {
		t.Fatal(out.Settings)
	}
	if time.Until(out.Deadline) > time.Second {
		t.Fatal("snapshot exceeded hard ceiling")
	}
	if _, err := scope.Query("SELECT 1"); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatal("scope survived handoff", err)
	}
	if rows.Next() || rows.Scan(new(int)) != ErrSnapshotClosed {
		t.Fatal("rows survived callback")
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("physical reader leaked")
	}
	if _, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); err != nil {
		t.Fatal(err)
	}
	// The role's mode=ro still denies a mutation submitted through Query.
	_, err = s.Read(t.Context(), snapshotRequest(), time.Second, func(r *ReadScope) error {
		rows, err := r.Query("DELETE FROM source RETURNING id")
		if rows != nil {
			rows.Close()
		}
		return err
	})
	if err == nil {
		t.Fatal("read scope was writable")
	}
}

func TestSnapshotSupersedeRetainsCustodyUntilCallbackReturns(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, _ := snapshotReaderDriver(t, path, g, a)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := s.Read(ctx, snapshotRequest(), time.Second, func(r *ReadScope) error {
			if err := snapshotQueryOne(r); err != nil {
				close(entered)
				return err
			}
			close(entered)
			<-release
			_, err := r.Query("SELECT 1")
			return err
		})
		done <- err
	}()
	<-entered
	cancel()
	if a.Usage()[Foreground][Reader] != 1 {
		t.Fatal("cancel returned physical reader/output custody")
	}
	out, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne)
	if err != ErrBusy || out.Refusal != nil {
		t.Fatal("pool queued behind cancelled callback", out, err)
	}
	close(release)
	if err = <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("superseded snapshot leaked")
	}
	if _, err = s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotNativeCancelReplacementAndPolicy(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, counted := snapshotReaderDriver(t, path, g, a)
	_, err := s.Read(t.Context(), snapshotRequest(), 30*time.Millisecond, func(r *ReadScope) error {
		rows, err := r.Query("WITH RECURSIVE seq(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM seq) SELECT sum(x) FROM seq")
		if err != nil {
			return err
		}
		defer rows.Close()
		if !rows.Next() {
			return rows.Err()
		}
		return errors.New("unbounded query completed")
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	before := counted.opened.Load()
	// An explicit read Tx may roll back and safely reuse its connection. Force
	// replacement as a separate boundary rather than assuming cancellation must
	// destroy it (database/sql/driver reset behaviour may legitimately differ).
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = sqliteconn.Assert(t.Context(), conn, s.config); err != nil {
		t.Fatal(err)
	}
	if err = conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
		t.Fatal(err)
	}
	conn.Close()
	out, err := s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne)
	if err != nil {
		t.Fatal(err)
	}
	if counted.opened.Load() <= before {
		t.Fatal("forced replacement was not exercised after native cancellation")
	}
	if out.Settings.ForeignKeys != 1 || out.Settings.TempStore != 1 || out.Settings.BusyTimeout != 0 || out.Settings.MmapSize != 0 || out.Settings.QueryOnly != 1 {
		t.Fatal(out.Settings)
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("native cancel leaked resource debt")
	}
}

func TestSnapshotAdmissionAndSimultaneousRowBound(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, _, counted := snapshotReaderDriver(t, path, g, a)
	r := snapshotRequest()
	r.Need[MemoryBytes] = 5 << 20
	out, err := s.Read(t.Context(), r, time.Second, snapshotQueryOne)
	if err != nil || out.Refusal == nil || out.Refusal.Code != Capacity || counted.opened.Load() != 0 {
		t.Fatal("allocation before admission", out, err)
	}
	_, err = s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		for i := 0; i < 8; i++ {
			if _, err := scope.Query("SELECT id FROM source"); err != nil {
				return err
			}
		}
		if _, err := scope.Query("SELECT 1"); err != ErrBusy {
			return errors.New("unbounded retained SQL rows")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if a.Usage() != [4]Amounts{} {
		t.Fatal("retained rows leaked")
	}
	s.Close()
	if _, err = s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); err != ErrSnapshotClosed {
		t.Fatal(err)
	}
}

func TestSnapshotRejectsPoolMismatchAndReplacesPolicyDrift(t *testing.T) {
	path, _ := snapshotFixture(t)
	g, a := snapshotGovernor(t)
	s, db, counted := snapshotReaderDriver(t, path, g, a)
	wrong := s.config
	wrong.Connections = 2
	if _, err := NewSnapshots(db, wrong, g, a); err == nil {
		t.Fatal("mismatched pool could wait behind a hidden connection queue")
	}
	_, foreign := snapshotGovernor(t)
	if _, err := NewSnapshots(db, s.config, g, foreign); err == nil {
		t.Fatal("foreign retirement capability accepted")
	}
	_, err := s.Read(t.Context(), snapshotRequest(), time.Second, func(scope *ReadScope) error {
		if _, ok := scope.Context().Deadline(); !ok {
			t.Fatal("no bounded context exposed")
		}
		rows, err := scope.Query("PRAGMA query_only=0")
		if err != nil {
			return err
		}
		return rows.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	before := counted.opened.Load()
	if _, err = s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); !errors.Is(err, sqliteconn.ErrPolicy) {
		t.Fatal("policy drift did not invalidate boundary", err)
	}
	if _, err = s.Read(t.Context(), snapshotRequest(), time.Second, snapshotQueryOne); err != nil {
		t.Fatal("connector replacement failed to restore policy", err)
	}
	if counted.opened.Load() <= before {
		t.Fatal("inconsistent physical connection was reused")
	}
	if a.Usage() != [4]Amounts{} || db.Stats().InUse != 0 {
		t.Fatal("drift path leaked resources")
	}
}
