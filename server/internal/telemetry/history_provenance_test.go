package telemetry

import (
	"context"
	"testing"
	"time"
)

func TestCounterWarmupAndResetHaveNoMeasuredZero(t *testing.T) {
	var cpu cpuCounter
	if m := deltaCPU(&cpu, 10, 100); m.Known() || m.Detail != warmingUp {
		t.Fatal("initial CPU counter became a measured value", m)
	}
	if m := deltaCPU(&cpu, 10, 200); m.Status != StatusAvailable || m.Value != 0 {
		t.Fatal("a real idle CPU interval lost its measured zero", m)
	}
	if m := deltaCPU(&cpu, 1, 1); m.Known() {
		t.Fatal("reset CPU counter became a measured rate", m)
	}
	if m := deltaCPU(&cpu, 1, 101); m.Status != StatusAvailable || m.Value != 0 {
		t.Fatal("CPU did not resume from the actual reset counter", m)
	}
	var previous counter
	for i, current := range []counter{{read: 100, write: 200, known: true}, {read: 100, write: 200, known: true}, {read: 1, write: 2, known: true}, {read: 1, write: 2, known: true}} {
		s := newSample(int64(i+1) * 2000)
		previous = rate(s, "diskRead", "diskWrite", previous, s.At, current, "")
		for _, name := range []string{"diskRead", "diskWrite"} {
			m := s.Metrics[name]
			if i%2 == 0 && m.Known() || i%2 == 1 && (m.Status != StatusAvailable || m.Value != 0) {
				t.Fatal("disk warm-up/reset or measured zero was mislabeled", i, name, m)
			}
		}
	}
}

func TestHistoryPointsCarryOwnProvenanceAndOmitWarmup(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	warm, measured := newSample(at), newSample(at+2000)
	var cpu cpuCounter
	warm.Metrics["cpu"] = deltaCPU(&cpu, 10, 100)
	measured.Metrics["cpu"] = deltaCPU(&cpu, 10, 200)
	measured.Metrics["gpuUsage"] = Limited(0, "Only one graphics engine is measured.")
	c := New(Options{Now: func() time.Time { return time.UnixMilli(at + 4000) }})
	c.detail = []Sample{warm, measured}
	for _, window := range []string{"live", "10m"} {
		out, ok := c.Read(window, "UTC", time.UnixMilli(at).UTC().Format(time.RFC3339Nano))
		if !ok || len(out.Series["cpu"]) != 1 {
			t.Fatal("warm-up was charted or the actual point was lost", window, out)
		}
		point := out.Series["cpu"][0]
		if point.T != measured.At || point.V != 0 || point.Status != PointStatus(StatusAvailable) || point.Detail != "" {
			t.Fatal("real zero was not an independently qualified point", point)
		}
		gpu := out.Series["gpuUsage"][0]
		if gpu.V != 0 || gpu.Status != PointStatus(StatusLimited) || gpu.Detail != measured.Metrics["gpuUsage"].Detail {
			t.Fatal("limited real zero lost its own provenance", gpu)
		}
	}
}

func TestRollupWeightsAndLimitedProvenanceDoNotInventMeasurements(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	first := []Sample{newSample(at), newSample(at + 2000)}
	first[0].Metrics["cpu"] = Unavailable(warmingUp)
	first[1].Metrics["cpu"] = Available(10)
	first[1].Metrics["gpuUsage"] = Limited(0, "Partial engine measurement.")
	second := []Sample{newSample(at + 60000), newSample(at + 62000), newSample(at + 64000)}
	for i := range second {
		second[i].Metrics["cpu"] = Available(20)
		second[i].Metrics["gpuUsage"] = Available(100)
	}
	a, b := average(first, at), average(second, at+60000)
	if a.counts["cpu"] != 1 || a.firstAt["cpu"] != at+2000 || a.Metrics["cpu"].Value != 10 {
		t.Fatal("unmeasured warm-up contaminated the actual average", a)
	}
	c := New(Options{Now: func() time.Time { return time.UnixMilli(at + 1800000) }})
	c.rollups = []Sample{a, b}
	out, ok := c.Read("24h", "UTC", "2026-01-01T00:30:00Z")
	if !ok || len(out.Series["cpu"]) != 1 || out.Series["cpu"][0].V != 17.5 {
		t.Fatal("rollup lost true sample weights", out)
	}
	if gpu := out.Series["gpuUsage"][0]; gpu.Status != PointStatus(StatusLimited) || gpu.Detail != "Partial engine measurement." || gpu.V != 75 {
		t.Fatal("mixed limited/available observations were upgraded", gpu)
	}
}

func TestPersistedLegacyRateProvenanceIsNotInferredOrLaundered(t *testing.T) {
	db := openTelemetryDatabase(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	for _, row := range []struct {
		name     string
		stamp    int64
		value    float64
		revision int
	}{{"cpu", at, 5, 0}, {"diskRead", at, 0, 0}, {"netIn", at, 0, 0}, {"memory", at, 50, 0}, {"cpu", at + 900000, 0, measurementRevision}} {
		if _, err := db.Exec(`INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES(?,?,1,?,'available','',?,?,?)`, row.stamp, row.name, row.value, row.stamp, row.stamp, row.revision); err != nil {
			t.Fatal(err)
		}
	}
	read := func() Reading {
		out, err := ReadAggregate(context.Background(), db, "24h", "UTC", "2026-01-01T00:30:00Z")
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(out Reading) {
		if len(out.Series["cpu"]) != 1 || out.Series["cpu"][0].V != 0 || out.Series["cpu"][0].Status != PointStatus(StatusAvailable) {
			t.Fatal("legacy rate was qualified or a proven measured zero was lost", out)
		}
		if len(out.Series["diskRead"]) != 0 || len(out.Series["netIn"]) != 0 || len(out.Series["memory"]) != 1 {
			t.Fatal("legacy rate ambiguity erased unrelated actual facts", out)
		}
	}
	check(read())
	// A new real sample must not relabel a mixed legacy quarter as revision 1.
	s := newSample(at + 120000)
	s.Metrics["cpu"] = Available(20)
	c := New(Options{DB: db, Now: func() time.Time { return time.UnixMilli(at + 1860000) }})
	c.rollups = []Sample{average([]Sample{s}, s.At)}
	c.persist(context.Background(), true)
	check(read())
	var revision, count, rows int
	if err := db.QueryRow(`SELECT measurement_revision,samples FROM telemetry_rollups WHERE bucket_ms=? AND metric='cpu'`, at).Scan(&revision, &count); err != nil || revision != 0 || count != 2 {
		t.Fatal("old provenance was overwritten or original data was discarded", revision, count, err)
	}
	if err := db.QueryRow(`SELECT count(*) FROM telemetry_rollups`).Scan(&rows); err != nil || rows != 5 {
		t.Fatal("projection repair deleted historical rows", rows, err)
	}
}

func TestPersistedPartialMeasurementKeepsItsDetailAcrossAvailableUpdates(t *testing.T) {
	db := openTelemetryDatabase(t)
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	a, b := newSample(at), newSample(at+60000)
	a.Metrics["gpuUsage"], b.Metrics["gpuUsage"] = Limited(0, "Partial engine measurement."), Available(100)
	c := New(Options{DB: db, Now: func() time.Time { return time.UnixMilli(at + 1800000) }})
	c.rollups = []Sample{average([]Sample{a}, a.At), average([]Sample{b}, b.At)}
	c.persist(context.Background(), true)
	out, err := ReadAggregate(context.Background(), db, "24h", "UTC", "2026-01-01T00:30:00Z")
	if err != nil || len(out.Series["gpuUsage"]) != 1 {
		t.Fatal(out, err)
	}
	if point := out.Series["gpuUsage"][0]; point.Status != PointStatus(StatusLimited) || point.V != 50 || point.Detail != "Partial engine measurement." {
		t.Fatal("available update erased the limited point's original provenance", point)
	}
}
