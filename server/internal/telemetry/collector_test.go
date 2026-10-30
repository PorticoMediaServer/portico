package telemetry

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/persistence"
	"strings"
	"testing"
	"time"

	_ "github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// fakeSampler produces a predictable ramp so rollup averages and window bounds
// can be asserted without touching the host.
type fakeSampler struct {
	calls int
	gpu   bool
}

func (f *fakeSampler) Sample(_ context.Context, at int64) Sample {
	f.calls++
	s := newSample(at)
	s.Metrics["cpu"] = Available(float64(f.calls))
	s.Metrics["memory"] = Available(50)
	s.Metrics["diskRead"] = Available(1000)
	s.Metrics["diskWrite"] = Available(2000)
	s.Metrics["netIn"] = Available(10)
	s.Metrics["netOut"] = Available(20)
	s.MemoryUsedBytes, s.MemoryTotalBytes = 4<<30, 8<<30
	if f.gpu {
		s.Metrics["gpuUsage"] = Available(33)
		s.GPUDevice, s.GPUProvider = "Test GPU", "Test"
	}
	return s
}

func openTelemetryDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, e := sql.Open("sqlite", t.TempDir()+"/telemetry.db")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { db.Close() })
	if _, e = db.Exec(`PRAGMA journal_mode=WAL`); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec(`CREATE TABLE play_history(started_ms INTEGER NOT NULL)`); e != nil {
		t.Fatal(e)
	}
	ddl := "CREATE TABLE telemetry_rollups(" + strings.Split(strings.Split(persistence.BaselineSQL(), "CREATE TABLE telemetry_rollups(")[1], ";")[0]
	if _, e = db.Exec(ddl); e != nil {
		t.Fatal(e)
	}
	return db
}
func TestQuarterTelemetryUsesActualWeightsAndExcludesCurrentQuarter(t *testing.T) {
	db := openTelemetryDatabase(t)
	clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := New(Options{Sampler: &fakeSampler{}, DB: db, Now: func() time.Time { return clock }})
	for n := 0; n < 32; n++ {
		c.Collect(context.Background())
		clock = clock.Add(2 * time.Second)
	}
	clock = time.Date(2026, 1, 1, 0, 15, 0, 0, time.UTC)
	c.Collect(context.Background())
	clock = clock.Add(time.Minute)
	c.Collect(context.Background())
	out, e := ReadAggregate(context.Background(), db, "24h", "Asia/Kathmandu", "2026-01-01T00:15:00Z")
	if e != nil {
		t.Fatal(e)
	}
	if len(out.Buckets) != 24 || len(out.Series["cpu"]) != 1 || out.Series["cpu"][0].V != 16.5 || len(out.Series["gpuUsage"]) != 0 {
		t.Fatal("invented/current-quarter or unweighted facts", out)
	}
	var count int
	if e = db.QueryRow(`SELECT samples FROM telemetry_rollups WHERE metric='cpu' AND bucket_ms=?`, time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()).Scan(&count); e != nil || count != 32 {
		t.Fatal(count, e)
	}
	restored := New(Options{Sampler: &fakeSampler{}, DB: db, Now: func() time.Time { return clock }})
	restored.Restore(context.Background())
	again, ok := restored.Read("24h", "Asia/Kathmandu", "2026-01-01T00:15:00Z")
	if !ok || again.Series["cpu"][0].V != 16.5 {
		t.Fatal(again)
	}
	old := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	if _, e = db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',1,10,'available','',?,?,1)`, old, old, old); e != nil {
		t.Fatal(e)
	}
	c.persist(context.Background(), true)
	all, e := ReadAggregate(context.Background(), db, "all", "Pacific/Chatham", "2026-01-01T00:15:00Z")
	if e != nil || len(all.Series["cpu"]) != 2 {
		t.Fatal("All-time15-minute retention", all, e)
	}
	if _, e = db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',1,1,'available','',?,?,1)`, old+1, old, old); e == nil {
		t.Fatal("non-quarter grain accepted")
	}
}
func TestQuarterTelemetryLateFactsAndSharedReadTransaction(t *testing.T) {
	db := openTelemetryDatabase(t)
	at := time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC).UnixMilli()
	read := func() Reading {
		out, e := ReadAggregate(context.Background(), db, "today", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
		if e != nil {
			t.Fatal(e)
		}
		return out
	}
	if len(read().Series["cpu"]) != 0 {
		t.Fatal("fake empty value")
	}
	if _, e := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',2,30,'available','',?,?,1)`, at, at, at+2000); e != nil {
		t.Fatal(e)
	}
	if len(read().Series["cpu"]) != 1 || read().Series["cpu"][0].V != 15 {
		t.Fatal("late event not included")
	}
	if _, e := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',1,99,'available','',?,?,1)`, at+900000, at+900000, at+900000); e != nil {
		t.Fatal(e)
	}
	if read().Series["cpu"][0].V != 15 {
		t.Fatal("exactly-at-until leaked")
	}
}
func TestLiveRetainsMovingSixtyMeasuredTwoSecondPoints(t *testing.T) {
	clock := time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)
	c := New(Options{Sampler: &fakeSampler{}, Now: func() time.Time { return clock }})
	for n := 0; n < 120; n++ {
		c.Collect(context.Background())
		clock = clock.Add(2 * time.Second)
	}
	out, ok := c.Read("live", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
	if !ok || len(out.Series["cpu"]) != 60 || out.Until == "2026-10-22T12:00:00Z" {
		t.Fatal(out)
	}
	for n, p := range out.Series["cpu"] {
		if n > 0 && p.T-out.Series["cpu"][n-1].T != 2000 {
			t.Fatal("live cadence", p)
		}
	}

	diagnostic, ok := c.Read("10m", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
	if !ok || diagnostic.Period != "10m" || diagnostic.Window != "10m" {
		t.Fatal("legacy detail metadata differs from requested window", diagnostic)
	}
}

func TestAggregateQueriesBorrowOneActualReadAndSeeLateFactsAfterward(t *testing.T) {
	db := openTelemetryDatabase(t)
	at := time.Date(2026, 10, 22, 11, 45, 0, 0, time.UTC).UnixMilli()
	if _, e := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,'cpu',2,30,'available','',?,?,1)`, at, at, at+2000); e != nil {
		t.Fatal(e)
	}
	e := dbwork.WithReadSnapshot(context.Background(), db, func(ctx context.Context) error {
		first, e := ReadAggregate(ctx, db, "today", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
		if e != nil {
			return e
		}
		if _, e = db.Exec(`UPDATE telemetry_rollups SET samples=3,total=60 WHERE metric='cpu'`); e != nil {
			return e
		}
		second, e := ReadAggregate(ctx, db, "today", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
		if e != nil {
			return e
		}
		if first.Series["cpu"][0].V != 15 || second.Series["cpu"][0].V != 15 {
			t.Fatal("independent transaction saw late write", first, second)
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
	fresh, e := ReadAggregate(context.Background(), db, "today", "Asia/Kathmandu", "2026-10-22T12:00:00Z")
	if e != nil || fresh.Series["cpu"][0].V != 20 {
		t.Fatal("fresh read froze late fact", fresh, e)
	}
}

func TestQuarterFirstEventUsesOnlyActualKnownMetricSamples(t *testing.T) {
	db := openTelemetryDatabase(t)
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC).UnixMilli()
	samples := []Sample{newSample(at), newSample(at + 2000), newSample(at + 4000)}
	samples[1].Metrics["cpu"] = Available(10)
	samples[2].Metrics["gpuUsage"] = Available(20)
	c := New(Options{DB: db, Now: func() time.Time { return time.UnixMilli(at + 60000) }})
	c.rollups = []Sample{average(samples, at)}
	c.persist(context.Background(), true)
	for _, tc := range []struct {
		name  string
		event int64
	}{{"cpu", at + 2000}, {"gpuUsage", at + 4000}} {
		var first, last, count int64
		if e := db.QueryRow(`SELECT first_event_ms,last_event_ms,samples FROM telemetry_rollups WHERE metric=?`, tc.name).Scan(&first, &last, &count); e != nil {
			t.Fatal(e)
		}
		if first != tc.event || last != tc.event || count != 1 {
			t.Fatal("unavailable sample became an event", tc, first, last, count)
		}
	}
	var first int64
	if e := db.QueryRow(`SELECT min(first_event_ms) FROM telemetry_rollups`).Scan(&first); e != nil || first != at+2000 {
		t.Fatal(first, e)
	}
}
