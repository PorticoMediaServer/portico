package read

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
)

// mpByteForecastSpy records only the reduced actual-byte forecast, never the
// full compatible reservation used by fixed-point first windows.
type mpByteForecastSpy struct {
	*governor.Snapshots
	forecast governor.SnapshotForecast
	ceiling  int64
}

func (s *mpByteForecastSpy) ReadForecasted(ctx context.Context, request governor.Request, budget time.Duration, forecast governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	if forecast.NativeRowBytes < s.NativeRowReservation() {
		s.forecast, s.ceiling = forecast, request.Need[governor.MemoryBytes]
	}
	return s.Snapshots.ReadForecasted(ctx, request, budget, forecast, read)
}

// mpByteBoundary interposes after every lookup window while the native view is
// still active, covering both the fixed first window and the armed business one.
type mpByteBoundary struct {
	*governor.Snapshots
	phase int
	after func(int)
}

func (s *mpByteBoundary) Read(ctx context.Context, r governor.Request, b time.Duration, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, err := s.Snapshots.Read(ctx, r, b, read)
	s.phase++
	if s.after != nil {
		s.after(s.phase)
	}
	return result, err
}

func (s *mpByteBoundary) ReadForecasted(ctx context.Context, r governor.Request, b time.Duration, f governor.SnapshotForecast, read func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	result, err := s.Snapshots.ReadForecasted(ctx, r, b, f, read)
	s.phase++
	if s.after != nil {
		s.after(s.phase)
	}
	return result, err
}

func mpPointWant(t *testing.T, f *browseFixture, sql string, args ...any) int64 {
	t.Helper()
	rows, err := f.conn.QueryContext(t.Context(), sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	raw := make([]any, len(columns))
	dest := make([]any, len(raw))
	for i := range raw {
		dest[i] = &raw[i]
	}
	if !rows.Next() {
		t.Fatal("missing payload row")
	}
	if err = rows.Scan(dest...); err != nil {
		t.Fatal(err)
	}
	var want int64
	for _, v := range raw {
		switch v := v.(type) {
		case string:
			want += int64(len(v))
		case []byte:
			want += int64(len(v))
		}
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	return want
}

func mpAssertForecast(t *testing.T, spy *mpByteForecastSpy, result BrowseReadResult, want, copyBytes int64) {
	t.Helper()
	if spy.forecast.NativeRowBytes != want || spy.forecast.MaxQueries != 3 || spy.forecast.GoCopyBytes != copyBytes {
		t.Fatalf("forecast %+v want native %d copy %d", spy.forecast, want, copyBytes)
	}
	if len(result.LookupWindows) != 2 {
		t.Fatalf("windows %d", len(result.LookupWindows))
	}
	window := result.LookupWindows[1]
	if window.NativeReservedBytes <= want || window.NativeReservedBytes >= spy.NativeRowReservation() || window.GoReservedBytes != spy.forecast.GoCopyBytes {
		t.Fatalf("business window did not reserve selected bytes: %+v want %d", window, want)
	}
}

func TestMPQueueHeadReservesActualBytes(t *testing.T) {
	f, r := mpQueueFixture(t)
	spy := &mpByteForecastSpy{Snapshots: f.snapshots}
	r.snapshots = spy
	entry, session := "é\x00snapshot", "漢字\x00session"
	f.exec(t, `UPDATE queues_v1 SET current_entry_id=?,session_id=? WHERE id='mp-queue'`, entry, session)
	want := mpPointWant(t, f, mpQueueDefinitions()[2].SQL, "token", "mp-queue")
	got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
	if err != nil || got.CurrentEntry != entry || got.Session != session {
		t.Fatalf("wide queue values %+v %v", got, err)
	}
	if spy.ceiling != foregroundRequest().Need[governor.MemoryBytes] {
		t.Fatalf("ceiling %d", spy.ceiling)
	}
	mpAssertForecast(t, spy, result, want, f.output.Request.Need[governor.MemoryBytes]/2)
}

func TestMPSessionHeadReservesActualBytes(t *testing.T) {
	f, r := mpSessionFixture(t)
	spy := &mpByteForecastSpy{Snapshots: f.snapshots}
	r.snapshots = spy
	end := "漢字\x00end"
	title := "Émission\x00" + strings.Repeat("界", 40)
	f.exec(t, `UPDATE playback_v1_sessions SET end_reason=? WHERE id='mp-session'`, end)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, title)
	want := mpPointWant(t, f, mpSessionDefinitions()[2].SQL, "token", "mp-session", int64(1), int64(1), int64(1))
	got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
	if err != nil || got.EndReason != end || got.Item.Title != title {
		t.Fatalf("wide session values %+v %v", got, err)
	}
	if spy.ceiling != foregroundRequest().Need[governor.MemoryBytes] {
		t.Fatalf("ceiling %d", spy.ceiling)
	}
	mpAssertForecast(t, spy, result, want, f.output.Request.Need[governor.MemoryBytes]/2)
}

func TestMPPreparedJobReservesActualBytes(t *testing.T) {
	f, r := mpPreparedFixture(t)
	spy := &mpByteForecastSpy{Snapshots: f.snapshots}
	r.snapshots = spy
	phase, code := "Émission\x00encoding", "漢\x00code"
	title := "Préparé\x00" + strings.Repeat("é", 60)
	f.exec(t, `UPDATE prepared_media_jobs SET phase=?,error_code=? WHERE id='mp-job'`, phase, code)
	f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, title)
	want := mpPointWant(t, f, mpPreparedDefinitions()[2].SQL, "mp-job", int64(1), int64(1), int64(1), "movies")
	got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
	if err != nil || got.Phase != phase || got.ErrorCode != code || got.Item.Title != title {
		t.Fatalf("wide prepared values %+v %v", got, err)
	}
	if spy.ceiling != foregroundRequest().Need[governor.MemoryBytes] {
		t.Fatalf("ceiling %d", spy.ceiling)
	}
	mpAssertForecast(t, spy, result, want, f.output.Request.Need[governor.MemoryBytes]/2)
}

// A selection larger than the snapshot's native row ceiling is refused by the
// narrow point, so the wide payload never opens and no partial value escapes.
func TestMPHeadsRefuseOversizedSelection(t *testing.T) {
	oversized := strings.Repeat("x", (8<<20)+1)
	t.Run("queue", func(t *testing.T) {
		f, r := mpQueueFixture(t)
		f.exec(t, `UPDATE queues_v1 SET current_entry_id=? WHERE id='mp-queue'`, oversized)
		got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
		if !errors.Is(err, governor.ErrSnapshotBudget) || got != (MediaQueueHead{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("queue oversized %+v %+v %v", got, result, err)
		}
	})
	t.Run("session", func(t *testing.T) {
		f, r := mpSessionFixture(t)
		f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, oversized)
		got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
		if !errors.Is(err, governor.ErrSnapshotBudget) || got != (MediaSessionHead{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("session oversized %+v %+v %v", got, result, err)
		}
	})
	t.Run("prepared", func(t *testing.T) {
		f, r := mpPreparedFixture(t)
		f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, oversized)
		got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
		if !errors.Is(err, governor.ErrSnapshotBudget) || got != (PreparedJobStatus{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("prepared oversized %+v %+v %v", got, result, err)
		}
	})
}

// Inadequate same-snapshot bytes refuse before the payload query, even when the
// payload statement itself is already registered.
func TestMPExactBytesRefuseBeforePayload(t *testing.T) {
	queue, _ := mpQueueFixture(t)
	session, _ := mpSessionFixture(t)
	prepared, _ := mpPreparedFixture(t)
	for _, point := range []struct {
		name  string
		args  []any
		spool *governor.Snapshots
	}{
		{"foreground.mp.queue_head", []any{"token", "mp-queue"}, queue.snapshots},
		{"foreground.mp.session_head", []any{"token", "mp-session", int64(1), int64(1), int64(1)}, session.snapshots},
		{"foreground.mp.prepared_job", []any{"mp-job", int64(1), int64(1), int64(1), "movies"}, prepared.snapshots},
	} {
		called := false
		_, err := point.spool.ReadForecasted(t.Context(), foregroundRequest(), time.Second, governor.SnapshotForecast{NativeRowBytes: 1, MaxQueries: 2, GoCopyBytes: 64 << 10}, func(scope *governor.ReadScope) error {
			called = true
			rows, _, err := queryReadScope(scope, point.name, point.args...)
			if !errors.Is(err, governor.ErrSnapshotBudget) || rows != nil {
				t.Fatal("payload decoded without admitted bytes", point.name, rows, err)
			}
			return err
		})
		if !called || !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatal(point.name, err)
		}
	}
}

// Length growth between the measuring and business windows retries in a fresh
// window; it is not reported as an exhausted request or a truncated value.
func TestMPHeadGrowthRetriesInFreshWindow(t *testing.T) {
	f, r := mpSessionFixture(t)
	title := "new\x00" + strings.Repeat("界", 50)
	r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
		if phase == 1 {
			f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, title)
		}
	}}
	got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
	if err != nil || result.Attempts != 2 || got.Item.Title != title {
		t.Fatalf("growth retry %+v %+v %v", got, result, err)
	}
}

// Cancellation between the point and business windows publishes nothing.
func TestMPHeadCancellationBetweenWindows(t *testing.T) {
	t.Run("session", func(t *testing.T) {
		f, r := mpSessionFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				cancel()
			}
		}}
		got, _, err := r.sessionHead(ctx, foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
		if !errors.Is(err, context.Canceled) || got != (MediaSessionHead{}) {
			t.Fatalf("session cancel %+v %v", got, err)
		}
	})
	t.Run("prepared", func(t *testing.T) {
		f, r := mpPreparedFixture(t)
		ctx, cancel := context.WithCancel(t.Context())
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				cancel()
			}
		}}
		got, _, err := r.preparedJob(ctx, foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
		if !errors.Is(err, context.Canceled) || got != (PreparedJobStatus{}) {
			t.Fatalf("prepared cancel %+v %v", got, err)
		}
	})
}

// Capture certifies only the exact projected point SQL and intended native
// shape. A same-ID statement with a different column or a dropped index is
// refused rather than silently reusing the registration.
func TestMPOctetCaptureRejectsChangedPoints(t *testing.T) {
	f := newForegroundSource(t)
	f.exec(t, mpQueueSeed)
	f.exec(t, mpSessionSeed)
	f.exec(t, mpPreparedSeed)
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpQueueSeed+mpSessionSeed+mpPreparedSeed)))
	fixture := browsePlanFixture(cfg, hash, plan.Unanalyzed)
	points := []stmt.Definition{mpQueueDefinitions()[1], mpSessionDefinitions()[1], mpPreparedDefinitions()[1]}
	for _, point := range points {
		for name, mutate := range map[string]func(*stmt.Definition){
			"column": func(d *stmt.Definition) {
				d.SQL = strings.Replace(d.SQL, "octet_length(", "octet_length(coalesce(", 1)
			},
			"plan": func(d *stmt.Definition) {
				d.Plan.Indexes, d.Plan.AutoIndexes, d.Plan.PrimaryKeys = nil, nil, nil
			},
		} {
			d := point
			mutate(&d)
			catalog, err := stmt.Describe([]stmt.Definition{d})
			if err != nil {
				continue
			}
			if _, err = plan.Capture(t.Context(), conn, catalog, fixture, policy); err == nil {
				t.Fatalf("%s %s: changed point certified", point.ID, name)
			}
		}
	}
}
