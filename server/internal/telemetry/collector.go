package telemetry

import (
	"context"
	"database/sql"
	"math"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/reportwindow"
	"sort"
	"strings"
	"sync"
	"time"
)

// Sampler produces one observation. The collector owns every call to it, so an
// implementation may be as slow as its own budget allows without a request ever
// waiting on it.
type Sampler interface {
	Sample(ctx context.Context, at int64) Sample
}

// Window bounds, in samples. Detail is two-second samples for ten minutes;
// rollups are one-minute averages for a day. Both are hard caps: the collector
// discards the oldest rather than growing.
const (
	DetailInterval = 2 * time.Second
	DetailSamples  = 300
	RollupInterval = time.Minute
	RollupSamples  = 24 * 60
	persistEvery   = time.Minute
	sampleBudget   = 1500 * time.Millisecond
	// Revision 1 excludes unmeasured counter warm-up/reset samples. Revision 0
	// rollups may have irreversibly averaged their placeholder zero into a rate.
	measurementRevision = 1
)

// Collector samples off the request path and answers windowed reads from
// memory. A nil database is supported: history then lives only for this process.
type Collector struct {
	sampler  Sampler
	db       *sql.DB
	now      func() time.Time
	interval time.Duration

	mu          sync.Mutex
	detail      []Sample
	rollups     []Sample
	minute      []Sample
	minuteStamp int64
	lastPersist int64
	persisted   int64
}

type Options struct {
	Sampler  Sampler
	DB       *sql.DB
	Now      func() time.Time
	Interval time.Duration
}

func New(o Options) *Collector {
	c := &Collector{sampler: o.Sampler, db: o.DB, now: o.Now, interval: o.Interval}
	if c.now == nil {
		c.now = time.Now
	}
	if c.interval <= 0 {
		c.interval = DetailInterval
	}
	if c.sampler == nil {
		c.sampler = HostSampler()
	}
	return c
}

// Run samples until the context ends. It is the only caller of the sampler.
func (c *Collector) Run(ctx context.Context) {
	c.Restore(ctx)
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	c.Collect(ctx)
	for {
		select {
		case <-ctx.Done():
			c.persist(context.WithoutCancel(ctx), true)
			return
		case <-ticker.C:
			c.Collect(ctx)
		}
	}
}

// Collect takes exactly one sample under a bounded budget and folds it into the
// rolling windows. Exported so a test drives the collector without a clock.
func (c *Collector) Collect(ctx context.Context) Sample {
	budget, cancel := context.WithTimeout(ctx, sampleBudget)
	defer cancel()
	at := c.now().UnixMilli()
	sample := c.sampler.Sample(budget, at)
	if sample.Metrics == nil {
		sample = newSample(at)
	}
	sample.At = at
	c.mu.Lock()
	c.detail = appendBounded(c.detail, sample, DetailSamples)
	stamp := at - at%RollupInterval.Milliseconds()
	if c.minuteStamp != 0 && stamp != c.minuteStamp {
		c.rollups = appendBounded(c.rollups, average(c.minute, c.minuteStamp), RollupSamples)
		c.minute = c.minute[:0]
	}
	c.minuteStamp = stamp
	c.minute = append(c.minute, sample)
	due := c.lastPersist == 0 || at-c.lastPersist >= persistEvery.Milliseconds()
	c.mu.Unlock()
	if due {
		c.persist(ctx, false)
	}
	return sample
}

func appendBounded(list []Sample, v Sample, limit int) []Sample {
	list = append(list, v)
	if len(list) > limit {
		list = append(list[:0], list[len(list)-limit:]...)
	}
	return list
}

// average folds a minute of detail samples into one rollup. A metric that no
// sample in the minute could measure stays unavailable rather than becoming a
// zero. Any contributing limited measurement keeps the result limited.
func average(samples []Sample, at int64) Sample {
	out := newSample(at)
	out.counts = map[string]int{}
	out.totals = map[string]float64{}
	out.firstAt, out.lastAt = map[string]int64{}, map[string]int64{}
	if len(samples) == 0 {
		return out
	}
	out.GPUDevice, out.GPUProvider = samples[len(samples)-1].GPUDevice, samples[len(samples)-1].GPUProvider
	out.MemoryUsedBytes, out.MemoryTotalBytes = samples[len(samples)-1].MemoryUsedBytes, samples[len(samples)-1].MemoryTotalBytes
	for _, name := range MetricNames {
		sum, count, status, detail := 0.0, 0, "", ""
		for _, s := range samples {
			m := s.Metrics[name]
			if !m.Known() {
				if status == "" {
					detail = m.Detail
				}
				continue
			}
			if count == 0 {
				out.firstAt[name] = s.At
			}
			out.lastAt[name] = s.At
			sum += m.Value
			count++
			if m.Status == StatusLimited || status == "" {
				status, detail = m.Status, m.Detail
			}
		}
		if count == 0 {
			out.Metrics[name] = Unavailable(detail)
			continue
		}
		out.Metrics[name] = Metric{Status: status, Value: sum / float64(count), Detail: detail}
		out.counts[name], out.totals[name] = count, sum
	}
	return out
}

// Latest is the most recent observation, or an all-unavailable sample before
// the first one lands.
func (c *Collector) Latest() Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.detail) == 0 {
		return newSample(c.now().UnixMilli())
	}
	return c.detail[len(c.detail)-1]
}

// Windows are the accepted aggregate views and existing moving detail views.
var Windows = []string{"live", "12h", "24h", "7d", "30d", "90d", "1y", "all", "today", "10m", "1h"}

func (c *Collector) Read(window, zone, until string) (Reading, bool) {
	if window == "live" || window == "10m" {
		r, e := reportwindow.Live(zone, until, c.now())
		if e != nil {
			return Reading{}, false
		}
		span := 2 * time.Minute
		if window == "10m" {
			span = 10 * time.Minute
			r.From = c.now().Add(-span).UTC().Format(time.RFC3339Nano)
			r.Buckets[0].Start = r.From
		}
		r.Period = window
		out := emptyReading(window, r)
		out.ObservedAt = c.now().UnixMilli()
		c.mu.Lock()
		defer c.mu.Unlock()
		for _, name := range MetricNames {
			for _, s := range c.detail {
				if s.At >= out.ObservedAt-span.Milliseconds() && s.At <= out.ObservedAt && s.Metrics[name].Known() {
					m := s.Metrics[name]
					out.Series[name] = append(out.Series[name], Point{T: s.At, V: m.Value, Status: PointStatus(m.Status), Detail: m.Detail})
				}
			}
			if len(c.detail) > 0 {
				out.Status[name] = c.detail[len(c.detail)-1].Metrics[name]
			}
			if window == "live" && len(out.Series[name]) > 60 {
				out.Series[name] = out.Series[name][len(out.Series[name])-60:]
			}
		}
		return out, true
	}
	if c.db != nil {
		out, e := ReadAggregate(context.Background(), c.db, window, zone, until)
		return out, e == nil
	}
	r, e := reportwindow.Resolve(window, zone, until, -1)
	if e != nil {
		return Reading{}, false
	}
	out := emptyReading(window, r)
	a := newAggregate(r)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, s := range c.rollups {
		if s.At >= r.FromMS() && s.At < r.UntilMS() {
			for _, name := range MetricNames {
				m := s.Metrics[name]
				if m.Known() {
					a.add(s.At, name, int64(s.counts[name]), s.totals[name], m.Status, m.Detail, measurementRevision)
				}
			}
		}
	}
	a.finish(&out)
	return out, true
}

func emptyReading(window string, r reportwindow.Interval) Reading {
	out := Reading{Interval: r, Window: window, Series: map[string][]Point{}, Status: map[string]Metric{}, ObservedAt: r.UntilMS()}
	for _, name := range MetricNames {
		out.Series[name] = []Point{}
		out.Status[name] = Unavailable("No measured fact in this interval.")
	}
	return out
}

type aggregateValue struct {
	count          int64
	total          float64
	status, detail string
}
type aggregate struct {
	interval reportwindow.Interval
	values   map[string][]aggregateValue
}

func newAggregate(r reportwindow.Interval) *aggregate {
	a := &aggregate{interval: r, values: map[string][]aggregateValue{}}
	for _, name := range MetricNames {
		a.values[name] = make([]aggregateValue, len(r.Buckets))
	}
	return a
}
func (a *aggregate) add(at int64, name string, count int64, total float64, status, detail string, revision int64) {
	if revision != measurementRevision {
		switch name {
		case "cpu", "diskRead", "diskWrite", "netIn", "netOut":
			// Old mixed averages cannot be unblended from t/v or their final
			// status. Preserve their rows, but do not assert measured rates.
			return
		}
	}
	values, known := a.values[name]
	if !known || count <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
		return
	}
	index, ok := a.interval.Index(max(at, a.interval.FromMS()))
	if !ok {
		return
	}
	v := &values[index]
	v.count += count
	v.total += total
	if status == StatusLimited || v.status == "" {
		v.status = status
		v.detail = detail
	}
}
func (a *aggregate) finish(out *Reading) {
	for _, name := range MetricNames {
		for n, v := range a.values[name] {
			if v.count > 0 {
				at, _ := time.Parse(time.RFC3339Nano, a.interval.Buckets[n].Start)
				m := Metric{Status: v.status, Value: v.total / float64(v.count), Detail: v.detail}
				out.Series[name] = append(out.Series[name], Point{T: at.UnixMilli(), V: m.Value, Status: PointStatus(m.Status), Detail: m.Detail})
				out.Status[name] = m
			}
		}
	}
}

// ReadAggregate joins the caller's actual read transaction when present. Only
// completed event-time quarters are eligible, independent of ingestion time.
func ReadAggregate(ctx context.Context, db *sql.DB, window, zone, until string) (Reading, error) {
	if dbwork.Snapshot(ctx) == nil {
		var out Reading
		e := dbwork.WithReadSnapshot(ctx, db, func(snapshot context.Context) error {
			var e error
			out, e = ReadAggregate(snapshot, db, window, zone, until)
			return e
		})
		return out, e
	}

	loc, at, e := reportwindow.Context(zone, until)
	_ = loc
	if e != nil {
		return Reading{}, e
	}
	first := int64(-1)
	if window == "all" {
		first, e = reportwindow.FirstFact(ctx, db, at.UnixMilli())
		if e != nil {
			return Reading{}, e
		}
	}
	r, e := reportwindow.Resolve(window, zone, until, first)
	if e != nil {
		return Reading{}, e
	}
	out := emptyReading(window, r)
	a := newAggregate(r)
	from := r.FromMS() - r.FromMS()%reportwindow.Grain.Milliseconds()
	rows, e := dbwork.Query(ctx, db, `SELECT bucket_ms,metric,samples,total,status,detail,measurement_revision FROM telemetry_rollups WHERE bucket_ms>=? AND bucket_ms<? ORDER BY bucket_ms,metric`, from, r.UntilMS())
	if e != nil {
		return out, e
	}
	defer rows.Close()
	for rows.Next() {
		var t, count, revision int64
		var name, status, detail string
		var total float64
		if e = rows.Scan(&t, &name, &count, &total, &status, &detail, &revision); e != nil {
			return out, e
		}
		a.add(t, name, count, total, status, detail, revision)
	}
	if e = rows.Err(); e != nil {
		return out, e
	}
	a.finish(&out)
	return out, nil
}

// Quarters are retained for All time, exceeding the thirteen-month minimum.
// No coarse UTC day loses a viewer-local month/hour boundary.
func (c *Collector) Restore(ctx context.Context) {
	if c.db == nil {
		return
	}
	var last sql.NullInt64
	if dbwork.QueryRow(ctx, c.db, `SELECT max(last_event_ms) FROM telemetry_rollups`).Scan(&last) != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if last.Valid {
		c.persisted = last.Int64 - last.Int64%60000
	}
}

// Closed-minute batches only carry real samples into their UTC quarter. The
// cursor is writer progress, not a report/ingestion snapshot. A restart leaves
// an interrupted minute absent instead of inventing its missing observations.
func (c *Collector) persist(ctx context.Context, final bool) {
	if c.db == nil {
		return
	}
	now := c.now().UnixMilli()
	c.mu.Lock()
	pending := []Sample{}
	for _, s := range c.rollups {
		if s.At > c.persisted {
			pending = append(pending, s)
		}
	}
	c.mu.Unlock()
	gated, e := dbwork.Begin(ctx, c.db, dbwork.ClassMaintenance)
	if e != nil {
		return
	}
	defer gated.Rollback()
	tx := gated.Tx()
	for _, s := range pending {
		values := []string{}
		args := []any{}
		for _, name := range MetricNames {
			m := s.Metrics[name]
			count, total := s.counts[name], s.totals[name]
			if !m.Known() || count <= 0 || math.IsNaN(total) || math.IsInf(total, 0) {
				continue
			}
			stamp := s.At - s.At%reportwindow.Grain.Milliseconds()
			values = append(values, "(?,?,?,?,?,?,?,?,?)")
			args = append(args, stamp, name, count, total, m.Status, m.Detail, s.firstAt[name], s.lastAt[name], measurementRevision)
		}
		if len(values) > 0 {
			// A sample has a fixed, bounded metric vocabulary. Persist all of
			// its known cells together, preserving each weight and event bound
			// without one statement per cell on an otherwise idle server.
			_, e = tx.ExecContext(ctx, `INSERT INTO telemetry_rollups(bucket_ms,metric,samples,total,status,detail,first_event_ms,last_event_ms,measurement_revision) VALUES`+strings.Join(values, ",")+` ON CONFLICT(bucket_ms,metric) DO UPDATE SET samples=samples+excluded.samples,total=total+excluded.total,status=CASE WHEN status='limited' OR excluded.status='limited' THEN 'limited' ELSE 'available' END,detail=CASE WHEN status='limited' THEN detail ELSE excluded.detail END,first_event_ms=min(first_event_ms,excluded.first_event_ms),last_event_ms=max(last_event_ms,excluded.last_event_ms),measurement_revision=min(measurement_revision,excluded.measurement_revision)`, args...)
			if e != nil {
				return
			}
		}
	}
	if gated.Commit() != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastPersist = now
	if len(pending) > 0 {
		c.persisted = pending[len(pending)-1].At
	}
}

func (c *Collector) Rollups() []Sample {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := append([]Sample{}, c.rollups...)
	sort.Slice(out, func(i, j int) bool { return out[i].At < out[j].At })
	return out
}
