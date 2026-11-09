package read

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/store/governor"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/readfixture"
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/sqliteconn"
	"github.com/PorticoMediaServer/portico/server/internal/store/schema"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt/plan"
	sqlite "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
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
	// C129's native row ceiling is 8 MiB + 64 KiB. Stay one byte beyond
	// that actual bound; 8 MiB + 1 is no longer an oversized selection.
	limits := readfixture.ReaderConfig().ReadLimits
	oversized := strings.Repeat("x", int(limits.Columns)*int(limits.LengthBytes)+1)
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
	t.Run("queue", func(t *testing.T) {
		f, r := mpQueueFixture(t)
		entry := "larger\x00" + strings.Repeat("界", 24)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE queues_v1 SET current_entry_id=? WHERE id='mp-queue'`, entry)
			}
		}}
		got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
		if err != nil || result.Attempts != 2 || got.CurrentEntry != entry {
			t.Fatalf("queue growth retry %+v %+v %v", got, result, err)
		}
	})
	t.Run("session", func(t *testing.T) {
		f, r := mpSessionFixture(t)
		title := "new\x00" + strings.Repeat("界", 50)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE catalog_entities SET title=? WHERE id=1`, title)
			}
		}}
		got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
		if err != nil || result.Attempts != 2 || got.Item.Title != title {
			t.Fatalf("session growth retry %+v %+v %v", got, result, err)
		}
	})
	t.Run("prepared", func(t *testing.T) {
		f, r := mpPreparedFixture(t)
		wide := "Émission\x00" + strings.Repeat("é", 70)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.exec(t, `UPDATE prepared_media_jobs SET phase=? WHERE id='mp-job'`, wide)
			}
		}}
		got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
		if err != nil || result.Attempts != 2 || got.Phase != wide {
			t.Fatalf("prepared growth retry %+v %+v %v", got, result, err)
		}
	})
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

// mpOctetCaptureSetup builds one combined MP fixture whose three seeds exercise
// every octet point against the same canonical schema.
func mpOctetCaptureSetup(t *testing.T) (*browseFixture, *sql.Conn, plan.Fixture, plan.IndexPolicy) {
	t.Helper()
	f := newForegroundSource(t)
	f.exec(t, mpQueueSeed)
	f.exec(t, mpSessionSeed)
	f.exec(t, mpPreparedSeed)
	cfg := browseReaderConfig()
	db, _, err := sqliteconn.Open(t.Context(), f.path, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	conn, err := db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	contract, err := schema.ReadContract("INT-INDEX-POLICY.json")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := plan.ReadIndexPolicy(contract)
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(readfixture.ForegroundHash(t)+mpQueueSeed+mpSessionSeed+mpPreparedSeed)))
	return f, conn, browsePlanFixture(cfg, hash, plan.Unanalyzed), policy
}

func mpOctetCapture(t *testing.T, conn *sql.Conn, fixture plan.Fixture, policy plan.IndexPolicy, point stmt.Definition) (plan.Golden, error) {
	t.Helper()
	catalog, err := stmt.Describe([]stmt.Definition{point})
	if err != nil {
		return plan.Golden{}, err
	}
	return plan.Capture(t.Context(), conn, catalog, fixture, policy)
}

// Capture certifies only the exact projected point SQL and intended native
// shape. A same-ID statement with a different column or a dropped index is
// refused rather than silently reusing the registration.
func TestMPOctetCaptureRejectsChangedPoints(t *testing.T) {
	_, conn, fixture, policy := mpOctetCaptureSetup(t)
	points := []stmt.Definition{mpQueueDefinitions()[1], mpSessionDefinitions()[1], mpPreparedDefinitions()[1]}
	mutations := map[string]func(*stmt.Definition){
		"column": func(d *stmt.Definition) {
			d.SQL = strings.Replace(d.SQL, "octet_length(", "octet_length(coalesce(", 1)
		},
		"plan": func(d *stmt.Definition) {
			d.Plan.Indexes, d.Plan.AutoIndexes, d.Plan.PrimaryKeys = nil, nil, nil
		},
		"nullable": func(d *stmt.Definition) { d.Params[0].Nullable = true },
		"kind":     func(d *stmt.Definition) { d.Kind = stmt.Write },
		"cold":     func(d *stmt.Definition) { d.Plan.Hot = false },
		"seeks":    func(d *stmt.Definition) { d.Plan.Seeks = []string{"id=?"} },
		"index":    func(d *stmt.Definition) { d.Plan.Indexes = append(d.Plan.Indexes, "other") },
		"origin":   func(d *stmt.Definition) { d.Plan.AutoIndexes[0].Origin = "u" },
		"collation": func(d *stmt.Definition) {
			d.Plan.AutoIndexes[0].Columns[0].Collation = "NOCASE"
		},
		"primary": func(d *stmt.Definition) { d.Plan.PrimaryKeys = nil },
		"waiver": func(d *stmt.Definition) {
			d.Plan.Waivers = []stmt.Waiver{{StatementID: d.ID, Stage: "eqp", Form: "scan", Match: "SCAN q"}}
		},
		"unknown": func(d *stmt.Definition) { d.ID = readStatementID("foreground.mp.unknown_octets") },
	}
	names := make([]string, 0, len(mutations))
	for name := range mutations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, point := range points {
		if _, err := mpOctetCapture(t, conn, fixture, policy, point); err != nil {
			t.Fatalf("%s: unchanged point refused: %v", point.ID, err)
		}
		before, err := json.Marshal(point)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range names {
			d := point
			// A struct copy shares slice backing arrays. Each mutation owns
			// its parameter/plan slices, including nested native key columns.
			// Cohorts and call sites are read-only in this mutation inventory.
			d.Params = append([]stmt.Param(nil), point.Params...)
			d.Plan.Indexes = append([]string(nil), point.Plan.Indexes...)
			d.Plan.Seeks = append([]string(nil), point.Plan.Seeks...)
			d.Plan.PrimaryKeys = append([]string(nil), point.Plan.PrimaryKeys...)
			d.Plan.AllowedScans = append([]string(nil), point.Plan.AllowedScans...)
			d.Plan.Waivers = append([]stmt.Waiver(nil), point.Plan.Waivers...)
			d.Plan.AutoIndexes = append([]stmt.AutoIndexBinding(nil), point.Plan.AutoIndexes...)
			for i := range d.Plan.AutoIndexes {
				d.Plan.AutoIndexes[i].Columns = append([]stmt.IndexColumn(nil), point.Plan.AutoIndexes[i].Columns...)
			}
			mutations[name](&d)
			after, err := json.Marshal(point)
			if err != nil || string(after) != string(before) {
				t.Fatalf("%s %s: mutation changed the original definition: %v", point.ID, name, err)
			}
			if _, err := mpOctetCapture(t, conn, fixture, policy, d); err == nil {
				t.Fatalf("%s %s: changed point certified", point.ID, name)
			}
		}
	}
}

// The native builtin audit must refuse a process that shadows octet_length,
// even when every ID/SQL/plan binding still matches.
func TestMPOctetPointRejectsShadowedFunction(t *testing.T) {
	const childFlag = "PORTICO_MP_OCTET_SHADOW_CHILD"
	if os.Getenv(childFlag) != "1" {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestMPOctetPointRejectsShadowedFunction$", "-test.count=1")
		cmd.Env = append(os.Environ(), childFlag+"=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native override child: %v\n%s", err, output)
		}
		return
	}
	if err := sqlite.RegisterDeterministicScalarFunction("OCTET_LENGTH", 1, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return int64(777), nil
	}); err != nil {
		t.Fatal(err)
	}
	_, conn, fixture, policy := mpOctetCaptureSetup(t)
	for _, point := range []stmt.Definition{mpQueueDefinitions()[1], mpSessionDefinitions()[1], mpPreparedDefinitions()[1]} {
		if _, err := mpOctetCapture(t, conn, fixture, policy, point); err == nil || !strings.Contains(err.Error(), "unshadowed") {
			t.Fatalf("%s: shadowed octet_length admitted: %v", point.ID, err)
		}
	}
}

// mpStaticSource is a browse snapshot source with a fixed native ceiling and no
// forecasted entry point. Helpers must refuse instead of widening silently.
type mpStaticSource struct{ bytes int64 }

func (mpStaticSource) Read(context.Context, governor.Request, time.Duration, func(*governor.ReadScope) error) (governor.SnapshotResult, error) {
	return governor.SnapshotResult{}, nil
}
func (s mpStaticSource) NativeRowReservation() int64 { return s.bytes }

func TestMPByteWindowRefusesUnbackedForecast(t *testing.T) {
	w := &mpByteWindow{source: mpStaticSource{bytes: 1024}}
	for _, tc := range []struct {
		name  string
		bytes int64
		calls uint16
	}{
		{"zero bytes", 0, 1},
		{"negative bytes", -1, 1},
		{"over reservation", 1025, 1},
		{"no calls", 1, 0},
	} {
		if err := w.arm(tc.bytes, tc.calls); !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatalf("%s arm %v", tc.name, err)
		}
	}
	if err := w.arm(1024, 3); err != nil || !w.pending || w.bytes != 1024 || w.calls != 3 {
		t.Fatalf("arm %v pending %t bytes %d calls %d", err, w.pending, w.bytes, w.calls)
	}
	if _, err := w.Read(t.Context(), foregroundRequest(), time.Second, func(*governor.ReadScope) error { return nil }); !errors.Is(err, ErrInput) {
		t.Fatalf("unbacked forecast %v", err)
	}
}

func TestMPPointBytesRejectsInvalidLengths(t *testing.T) {
	if got, err := mpPointBytes(nil); err != nil || got != 0 {
		t.Fatalf("empty %d %v", got, err)
	}
	if got, err := mpPointBytes([]any{int64(3), nil, int64(4)}); err != nil || got != 7 {
		t.Fatalf("nullable %d %v", got, err)
	}
	for _, row := range [][]any{
		{"3"},
		{int64(-1)},
		{int64(1 << 61), (int64(1) << 61) + 1},
	} {
		if _, err := mpPointBytes(row); !errors.Is(err, governor.ErrSnapshotBudget) {
			t.Fatalf("invalid lengths %v accepted: %v", row, err)
		}
	}
}

func TestMPByteGrowthClassification(t *testing.T) {
	query := func(rows [][]any, err error) browseQuery {
		return func(string, ...any) ([][]any, error) { return rows, err }
	}
	sentinel := errors.New("point failed")
	for _, tc := range []struct {
		name  string
		bytes int64
		rows  [][]any
		err   error
		want  error
	}{
		{"unmeasured", 0, nil, nil, governor.ErrSnapshotBudget},
		{"growth", 100, [][]any{{int64(120)}}, nil, ErrStale},
		{"same length", 100, [][]any{{int64(100)}}, nil, governor.ErrSnapshotBudget},
		{"oversized growth", 100, [][]any{{int64(1001)}}, nil, governor.ErrSnapshotBudget},
		{"missing point", 100, nil, nil, ErrDenied},
		{"ambiguous point", 100, [][]any{{int64(1)}, {int64(2)}}, nil, ErrDenied},
		{"invalid length", 100, [][]any{{"wide"}}, nil, governor.ErrSnapshotBudget},
		{"point failure", 100, nil, sentinel, sentinel},
	} {
		w := &mpByteWindow{source: mpStaticSource{bytes: 1000}, bytes: tc.bytes}
		err := mpByteGrowth(w, query(tc.rows, tc.err), "foreground.mp.test_octets")
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: %v want %v", tc.name, err, tc.want)
		}
	}
}

func TestMPBytePointRegistrationRejectsInvalid(t *testing.T) {
	for name, tc := range map[string]struct {
		payload string
		point   mpBytePoint
	}{
		"empty payload":   {"", mpBytePoint{lengths: "lengths", columns: []int{0}}},
		"empty lengths":   {"foreground.mp.test_octets", mpBytePoint{lengths: "", columns: []int{0}}},
		"empty columns":   {"foreground.mp.test_octets", mpBytePoint{lengths: "lengths"}},
		"negative column": {"foreground.mp.test_octets", mpBytePoint{lengths: "lengths", columns: []int{-1}}},
		"wide column":     {"foreground.mp.test_octets", mpBytePoint{lengths: "lengths", columns: []int{128}}},
		"duplicate":       {"foreground.mp.test_octets", mpBytePoint{lengths: "lengths", columns: []int{1, 1}}},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("invalid byte point accepted")
				}
			}()
			registerMPBytePoint(tc.payload, tc.point)
		})
	}
}

// A source pin with no matching row refuses with zero output before any payload
// window; a guessed ID can never publish an empty or partial head.
func TestMPHeadsRefuseMissingSelection(t *testing.T) {
	t.Run("queue", func(t *testing.T) {
		f, r := mpQueueFixture(t)
		got, result, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "missing"})
		if !errors.Is(err, ErrDenied) || got != (MediaQueueHead{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("queue missing %+v %+v %v", got, result, err)
		}
	})
	t.Run("session", func(t *testing.T) {
		f, r := mpSessionFixture(t)
		got, result, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "missing", Item: 1})
		if !errors.Is(err, ErrDenied) || got != (MediaSessionHead{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("session missing %+v %+v %v", got, result, err)
		}
	})
	t.Run("prepared", func(t *testing.T) {
		f, r := mpPreparedFixture(t)
		got, result, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "missing", Item: 1})
		if !errors.Is(err, ErrDenied) || got != (PreparedJobStatus{}) || len(result.LookupWindows) != 1 {
			t.Fatalf("prepared missing %+v %+v %v", got, result, err)
		}
	})
}

// Cancelling the retained-output grant after the measuring window must publish
// nothing and surface the grant refusal, never a partial head.
func TestMPHeadGrantCancellation(t *testing.T) {
	t.Run("queue", func(t *testing.T) {
		f, r := mpQueueFixture(t)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.governor.Cancel(f.output.Grant)
			}
		}}
		got, _, err := r.queueHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaQueueInput{View: f.input, Queue: "mp-queue"})
		if !errors.Is(err, governor.ErrGrant) || got != (MediaQueueHead{}) {
			t.Fatalf("queue grant cancel %+v %v", got, err)
		}
	})
	t.Run("session", func(t *testing.T) {
		f, r := mpSessionFixture(t)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.governor.Cancel(f.output.Grant)
			}
		}}
		got, _, err := r.sessionHead(t.Context(), foregroundRequest(), time.Second, f.output, MediaSessionInput{View: f.input, Session: "mp-session", Item: 1})
		if !errors.Is(err, governor.ErrGrant) || got != (MediaSessionHead{}) {
			t.Fatalf("session grant cancel %+v %v", got, err)
		}
	})
	t.Run("prepared", func(t *testing.T) {
		f, r := mpPreparedFixture(t)
		r.snapshots = &mpByteBoundary{Snapshots: f.snapshots, after: func(phase int) {
			if phase == 1 {
				f.governor.Cancel(f.output.Grant)
			}
		}}
		got, _, err := r.preparedJob(t.Context(), foregroundRequest(), time.Second, f.output, PreparedJobInput{View: f.input, Job: "mp-job", Item: 1})
		if !errors.Is(err, governor.ErrGrant) || got != (PreparedJobStatus{}) {
			t.Fatalf("prepared grant cancel %+v %v", got, err)
		}
	})
}
