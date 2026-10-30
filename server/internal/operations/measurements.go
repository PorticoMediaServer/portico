package operations

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log"
	"net/http"
	"os"

	"github.com/PorticoMediaServer/portico/apikit/httpclientclose"
	"github.com/PorticoMediaServer/portico/server/internal/apiwire"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/storagepaths"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Fact struct {
	State  string `json:"state"`
	Value  any    `json:"value"`
	Unit   string `json:"unit,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type Measurement struct {
	Name       string          `json:"name"`
	ObservedAt apiwire.Instant `json:"observedAt"`
	FreshUntil apiwire.Instant `json:"freshUntil"`
	Facts      map[string]Fact `json:"facts"`
}

func measured(v any, unit string) Fact { return Fact{State: "available", Value: v, Unit: unit} }
func unavailable(reason string) Fact   { return Fact{State: "unavailable", Value: nil, Reason: reason} }

// ProcessCPU is reserved for the owner diagnostics snapshot, not the ordinary
// resources panel.
func ProcessCPU() Fact { return processCPU() }

// Counters measure this HTTP process, not inferred host bandwidth/capacity.
type Measurements struct {
	DB             *sql.DB
	StateDirectory string
	Started        time.Time
	// Host is the telemetry collector's latest host sample, supplied by the HTTP
	// layer. Nil until telemetry has been read at least once, which the resources
	// panel reports as unavailable rather than as zero.
	Host func() telemetry.Sample
	// ActiveTranscodes reads the playback service's current converter ownership.
	// It is a callback so an ordinary panel read never starts a worker.
	ActiveTranscodes func(context.Context) (int, error)
	Clock            func() time.Time
	Requests         atomic.Int64
	Sent             atomic.Int64
	Received         atomic.Int64
	Inflight         atomic.Int64
	networkMu        sync.Mutex
	networkAt        time.Time
	networkIn        int64
	networkOut       int64
	inRate           float64
	outRate          float64
	rateReady        bool
	TimeoutAlert     func(context.Context, bool) error
	// Pattern resolves the request's registered route pattern (e.g.
	// "GET /v1/items/{id}") for log lines. It is set by the HTTP layer to
	// httpapi.RoutePattern; nil falls back to the request path without the
	// query. A pattern never carries identifiers, tokens or query strings.
	Pattern         func(*http.Request) string
	timeoutMu       sync.Mutex
	timeouts        []time.Time
	timeoutOpen     bool
	timeoutTimer    *time.Timer
	timeoutSequence uint64
	alertMu         sync.Mutex
	// VolumeProbe defaults to mediaVolume. Tests can supply a stalled mount
	// without depending on an actual network filesystem.
	VolumeProbe  func(string) (string, uint64, error)
	volumeMu     sync.Mutex
	volumeProbes map[string]*volumeProbe

	failMu   sync.Mutex
	failNow  func() time.Time
	failLog  func(string, ...any)
	failSeen map[string]*httpFailureWindow
}

// httpFailureWindow is one rate-limit bucket: the first 5xx of each
// (pattern, status, code) logs at once, the rest collapse to at most one line
// per minute with the suppressed count.
type httpFailureWindow struct {
	started    time.Time
	suppressed int
}

const httpFailureWindowLength = time.Minute
const httpFailureClasses = 128

// hostFacts projects the telemetry sample into the panel's fact vocabulary. The
// headline figures only: the charts read GET /v1/admin/telemetry instead.
func hostFacts(latest func() telemetry.Sample) map[string]Fact {
	out := map[string]Fact{}
	names := map[string]struct{ key, unit string }{
		"cpu":        {"hostCPU", "percent"},
		"memory":     {"hostMemory", "percent"},
		"gpuUsage":   {"gpuUsage", "percent"},
		"gpuMemory":  {"gpuMemory", "percent"},
		"gpuEncoder": {"gpuEncoder", "percent"},
	}
	var sample telemetry.Sample
	if latest != nil {
		sample = latest()
	}
	if sample.Metrics == nil {
		for _, target := range names {
			out[target.key] = unavailable("Host telemetry has not been sampled on this server yet.")
		}
		return out
	}
	for metric, target := range names {
		value := sample.Metrics[metric]
		switch value.Status {
		case telemetry.StatusAvailable:
			out[target.key] = measured(value.Value, target.unit)
		case telemetry.StatusLimited:
			out[target.key] = Fact{State: telemetry.StatusLimited, Value: value.Value, Unit: target.unit, Reason: value.Detail}
		default:
			out[target.key] = unavailable(value.Detail)
		}
	}
	if sample.MemoryTotalBytes > 0 {
		out["hostMemoryUsedBytes"] = measured(sample.MemoryUsedBytes, "bytes")
		out["hostMemoryTotalBytes"] = measured(sample.MemoryTotalBytes, "bytes")
	}
	if sample.GPUDevice != "" {
		out["gpuDevice"] = measured(sample.GPUDevice, "")
	}
	return out
}

func NewMeasurements(db *sql.DB, state string) *Measurements {
	m := &Measurements{DB: db, StateDirectory: state, Started: time.Now(), Clock: time.Now}
	m.TimeoutAlert = func(ctx context.Context, active bool) error {
		return New(db).Alert(ctx, "server_overloaded", "warning", active)
	}
	m.failNow = time.Now
	m.failLog = log.Printf
	m.failSeen = map[string]*httpFailureWindow{}
	return m
}

// requestPattern names the route by its registered pattern (which carries its
// method, e.g. "GET /v1/items/{id}"), never by a concrete path: a request no
// route matched is "<METHOD> (unmatched)". Only without a resolver (tests,
// early startup) is the bare path used, never the query, body or headers.
func (m *Measurements) requestPattern(r *http.Request) string {
	if r == nil {
		return ""
	}
	if m != nil && m.Pattern != nil {
		pattern := m.Pattern(r)
		switch {
		case pattern == "":
			return r.Method + " (unmatched)"
		case strings.HasPrefix(pattern, r.Method+" "):
			return pattern
		}
		return r.Method + " " + pattern
	}
	return r.Method + " " + r.URL.Path
}

// httpFailureCode pulls the error code out of a response body sample such as
// {"error":{"code":"timeout",...}}. The sample is a body prefix, so the code
// is scanned textually rather than decoded; anything unparseable or unbounded
// answers "unknown" rather than logging body bytes.
func httpFailureCode(sample []byte) string {
	marker := []byte(`"code"`)
	at := bytes.Index(sample, marker)
	if at < 0 {
		return "unknown"
	}
	rest := sample[at+len(marker):]
	rest = bytes.TrimLeft(rest, " \t\r\n:")
	if len(rest) < 3 || rest[0] != '"' {
		return "unknown"
	}
	rest = rest[1:]
	end := bytes.IndexByte(rest, '"')
	if end <= 0 || end > 64 {
		return "unknown"
	}
	code := string(rest[:end])
	for _, c := range code {
		if c < 0x20 || c == 0x7f {
			return "unknown"
		}
	}
	return code
}

// reportHTTPFailure logs one line for a 5xx the server answered, rate-limited
// by (pattern, status, code): the first at once, then at most one line per
// minute naming how many more were suppressed.
func (m *Measurements) reportHTTPFailure(route string, status int, code string) {
	if m == nil {
		return
	}
	m.failMu.Lock()
	if m.failSeen == nil {
		m.failSeen = map[string]*httpFailureWindow{}
	}
	now := time.Now()
	if m.failNow != nil {
		now = m.failNow()
	}
	key := route + "\x00" + strconv.Itoa(status) + "\x00" + code
	current, ok := m.failSeen[key]
	if ok && now.Sub(current.started) < httpFailureWindowLength {
		current.suppressed++
		m.failMu.Unlock()
		return
	}
	suppressed := 0
	if ok {
		suppressed = current.suppressed
		current.started, current.suppressed = now, 0
	} else {
		if len(m.failSeen) >= httpFailureClasses {
			for k, v := range m.failSeen {
				if now.Sub(v.started) >= httpFailureWindowLength {
					delete(m.failSeen, k)
				}
			}
			if len(m.failSeen) >= httpFailureClasses {
				clear(m.failSeen)
			}
		}
		m.failSeen[key] = &httpFailureWindow{started: now}
	}
	logf := m.failLog
	if logf == nil {
		logf = log.Printf
	}
	m.failMu.Unlock()
	if status == httpclientclose.Status {
		if suppressed > 0 {
			logf("[http] info: %s client closed (499); %d more like this since the last report", route, suppressed)
		} else {
			logf("[http] info: %s client closed (499)", route)
		}
		return
	}
	if suppressed > 0 {
		logf("[http] warn: %s answered %d %s; %d more like this since the last report", route, status, code, suppressed)
		return
	}
	logf("[http] warn: %s answered %d %s", route, status, code)
}

// These rates are averages over the interval between console measurements.
// There is no rate until a second sample establishes an interval.
func (m *Measurements) networkRates() (float64, float64, bool) {
	now := time.Now()
	if m.Clock != nil {
		now = m.Clock()
	}
	in, out := m.Received.Load(), m.Sent.Load()
	m.networkMu.Lock()
	defer m.networkMu.Unlock()
	if m.networkAt.IsZero() {
		m.networkAt, m.networkIn, m.networkOut = now, in, out
		return 0, 0, false
	}
	if elapsed := now.Sub(m.networkAt).Seconds(); elapsed >= 1 {
		m.inRate = float64(max(0, in-m.networkIn)) / elapsed
		m.outRate = float64(max(0, out-m.networkOut)) / elapsed
		m.rateReady = true
		m.networkAt, m.networkIn, m.networkOut = now, in, out
	}
	return m.inRate, m.outRate, m.rateReady
}

type mediaVolumeSpace struct {
	Volume         string   `json:"volume"`
	LibraryIDs     []string `json:"libraryIds"`
	AvailableBytes uint64   `json:"availableBytes"`
}

type volumeProbe struct {
	done   chan struct{}
	volume string
	free   uint64
	err    error
	at     time.Time
}

func (m *Measurements) startVolumeProbe(root string) *volumeProbe {
	m.volumeMu.Lock()
	if m.volumeProbes == nil {
		m.volumeProbes = map[string]*volumeProbe{}
	}
	if previous := m.volumeProbes[root]; previous != nil {
		select {
		case <-previous.done:
			if time.Since(previous.at) < 30*time.Second {
				m.volumeMu.Unlock()
				return previous
			}
		default:
			// A blocked statfs may not be cancellable. Keep one in-flight
			// goroutine per root rather than spawning one per panel request.
			m.volumeMu.Unlock()
			return previous
		}
	}
	probe := &volumeProbe{done: make(chan struct{})}
	m.volumeProbes[root] = probe
	m.volumeMu.Unlock()
	supervise.Go("operations.volume-probe", func() {
		probe.err = context.Canceled
		defer func() {
			probe.at = time.Now()
			close(probe.done)
		}()
		measure := m.VolumeProbe
		if measure == nil {
			measure = mediaVolume
		}
		probe.volume, probe.free, probe.err = measure(root)
	})
	return probe
}

func (m *Measurements) mediaVolumes(ctx context.Context) Fact {
	rows, err := m.DB.QueryContext(ctx, `SELECT id,root FROM libraries ORDER BY id`)
	if err != nil {
		return unavailable("Media library volumes could not be read.")
	}
	type libraryRoot struct{ id, root string }
	var roots []libraryRoot
	for rows.Next() {
		var entry libraryRoot
		if err = rows.Scan(&entry.id, &entry.root); err != nil {
			break
		}
		roots = append(roots, entry)
	}
	if err == nil {
		err = rows.Err()
	}
	closeErr := rows.Close()
	if err != nil || closeErr != nil {
		return unavailable("Media library volumes could not be read.")
	}
	// Every pooled read connection is now released before any filesystem call.
	probes := make([]*volumeProbe, len(roots))
	for i, entry := range roots {
		probes[i] = m.startVolumeProbe(entry.root)
	}
	waitCtx, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer cancel()
	groups := map[string]*mediaVolumeSpace{}
	pending := 0
	for i, entry := range roots {
		probe := probes[i]
		select {
		case <-probe.done:
		case <-waitCtx.Done():
			pending++
			continue
		}
		if probe.err != nil {
			continue // A missing source is not a free-space measurement.
		}
		if groups[probe.volume] == nil {
			groups[probe.volume] = &mediaVolumeSpace{Volume: probe.volume, LibraryIDs: []string{}, AvailableBytes: probe.free}
		}
		groups[probe.volume].LibraryIDs = append(groups[probe.volume].LibraryIDs, entry.id)
	}
	if len(groups) == 0 && pending > 0 {
		return unavailable("Media library volumes are still being measured.")
	}
	out := make([]mediaVolumeSpace, 0, len(groups))
	for _, group := range groups {
		out = append(out, *group)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Volume < out[j].Volume })
	return measured(out, "bytes")
}

type countedWriter struct {
	http.ResponseWriter
	m       *Measurements
	status  int
	request context.Context
	sample  []byte
}

func (w *countedWriter) WriteHeader(status int) {
	if httpclientclose.Context(w.request) {
		status = httpclientclose.Status
	}
	if w.status == 0 {
		w.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *countedWriter) Write(p []byte) (int, error) {
	if httpclientclose.Context(w.request) {
		if w.status == 0 {
			w.WriteHeader(httpclientclose.Status)
		}
		return 0, w.request.Err()
	}
	if w.status == 0 {
		w.status = http.StatusOK
	}
	if w.status >= 500 && len(w.sample) < 512 {
		w.sample = append(w.sample, p[:min(len(p), 512-len(w.sample))]...)
	}
	n, e := w.ResponseWriter.Write(p)
	w.m.Sent.Add(int64(n))
	return n, e
}
func (w *countedWriter) Unwrap() http.ResponseWriter     { return w.ResponseWriter }
func (w *countedWriter) RequestContext() context.Context { return w.request }
func (w *countedWriter) Flush()                          { _ = http.NewResponseController(w.ResponseWriter).Flush() }

type countedBody struct {
	io.ReadCloser
	m *Measurements
}

func (b countedBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	b.m.Received.Add(int64(n))
	return n, e
}
func (m *Measurements) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.Requests.Add(1)
		m.Inflight.Add(1)
		defer m.Inflight.Add(-1)
		if r.Body != nil {
			r.Body = countedBody{r.Body, m}
		}
		counted := &countedWriter{ResponseWriter: w, m: m, request: r.Context()}
		next.ServeHTTP(counted, r)
		if httpclientclose.Context(r.Context()) {
			// The race loser is an ordinary disconnect, even if a handler
			// attempted its usual unavailable/timeout response after cancellation.
			counted.status = httpclientclose.Status
			m.reportHTTPFailure(m.requestPattern(r), httpclientclose.Status, "client_closed")
			return
		}
		if counted.status >= 500 {
			m.reportHTTPFailure(m.requestPattern(r), counted.status, httpFailureCode(counted.sample))
		}
		if (counted.status == 503 || counted.status == 504) && bytes.Contains(counted.sample, []byte(`"code":"timeout"`)) {
			log.Printf("WARN: request timed out (%s)", m.requestPattern(r))
			m.recordTimeout()
		}
	})
}

const overloadWindow = 5 * time.Minute

func (m *Measurements) recordTimeout() {
	now := time.Now()
	if m.Clock != nil {
		now = m.Clock()
	}
	m.timeoutMu.Lock()
	m.timeouts = append(m.timeouts, now)
	m.pruneTimeoutsLocked(now)
	if len(m.timeouts) > 20 && !m.timeoutOpen {
		m.timeoutOpen = true
		m.timeoutSequence++
		m.publishTimeoutAlert(m.timeoutSequence, true)
	}
	m.scheduleTimeoutCheckLocked(now)
	m.timeoutMu.Unlock()
}

func (m *Measurements) pruneTimeoutsLocked(now time.Time) {
	cutoff := now.Add(-overloadWindow)
	first := 0
	for first < len(m.timeouts) && !m.timeouts[first].After(cutoff) {
		first++
	}
	if first > 0 {
		m.timeouts = append([]time.Time(nil), m.timeouts[first:]...)
	}
}

func (m *Measurements) scheduleTimeoutCheckLocked(now time.Time) {
	if !m.timeoutOpen || len(m.timeouts) == 0 {
		if m.timeoutTimer != nil {
			m.timeoutTimer.Stop()
		}
		return
	}
	delay := m.timeouts[0].Add(overloadWindow).Sub(now) + time.Millisecond
	if delay < time.Millisecond {
		delay = time.Millisecond
	}
	if m.timeoutTimer == nil {
		m.timeoutTimer = time.AfterFunc(delay, m.expireTimeouts)
	} else {
		m.timeoutTimer.Reset(delay)
	}
}

func (m *Measurements) expireTimeouts() {
	now := time.Now()
	if m.Clock != nil {
		now = m.Clock()
	}
	m.timeoutMu.Lock()
	m.pruneTimeoutsLocked(now)
	if m.timeoutOpen && len(m.timeouts) <= 20 {
		m.timeoutOpen = false
		m.timeoutSequence++
		m.publishTimeoutAlert(m.timeoutSequence, false)
	}
	m.scheduleTimeoutCheckLocked(now)
	m.timeoutMu.Unlock()
}

// Alert writes are rare and independent of the response. Sequence checks and
// serialization make a later auto-resolution win over a slower opening write.
func (m *Measurements) publishTimeoutAlert(sequence uint64, active bool) {
	if m.TimeoutAlert == nil {
		return
	}
	supervise.Go("operations.timeout-alert", func() {
		m.alertMu.Lock()
		defer m.alertMu.Unlock()
		m.timeoutMu.Lock()
		current := sequence == m.timeoutSequence
		m.timeoutMu.Unlock()
		if !current {
			return
		}
		ctx, cancel := context.WithTimeout(dbwork.WithClass(context.Background(), dbwork.ClassMaintenance), 5*time.Second)
		defer cancel()
		if err := m.TimeoutAlert(ctx, active); err != nil {
			log.Printf("Overload alert could not be updated: %v", err)
		}
	})
}

func (m *Measurements) Read(ctx context.Context, name string) (Measurement, error) {
	now := time.Now().UnixMilli()
	out := Measurement{Name: name, ObservedAt: apiwire.Instant(now), FreshUntil: apiwire.Instant(now + 30000), Facts: map[string]Fact{}}
	switch name {
	case "health":
		out.Facts["uptime"] = measured(int64(time.Since(m.Started).Seconds()), "seconds")
		if e := m.DB.PingContext(ctx); e != nil {
			out.Facts["database"] = unavailable("Database probe failed.")
		} else {
			out.Facts["database"] = measured("readable", "")
		}
		var count int
		e := m.DB.QueryRowContext(ctx, `SELECT count(*) FROM console_alerts WHERE status='open'`).Scan(&count)
		if e != nil {
			out.Facts["openAlerts"] = unavailable("Alert store could not be read.")
		} else {
			out.Facts["openAlerts"] = measured(count, "alerts")
		}
		out.Facts["readiness"] = measured("HTTP request served", "")
	case "resources":
		// Host and GPU facts come from the telemetry collector's last sample, not
		// from sampling here: a panel read must never start a reporter process.
		for key, fact := range hostFacts(m.Host) {
			out.Facts[key] = fact
		}
	case "storage":
		out.Facts["stateVolumeAvailable"] = volumeAvailable(m.StateDirectory)
		out.Facts["mediaVolumes"] = m.mediaVolumes(ctx)
		for key, file := range map[string]string{"databaseBytes": "server.sqlite", "databaseWALBytes": "server.sqlite-wal"} {
			if m.StateDirectory == "" {
				out.Facts[key] = unavailable("State directory not registered.")
				continue
			}
			f, e := os.Stat(storagepaths.Path(m.StateDirectory, file))
			if e != nil {
				out.Facts[key] = unavailable("File measurement unavailable.")
			} else {
				out.Facts[key] = measured(f.Size(), "bytes")
			}
		}
		for key, value := range processIO() {
			out.Facts[key] = value
		}
	case "network":
		inRate, outRate, rateReady := m.networkRates()
		out.Facts["httpRequests"] = measured(m.Requests.Load(), "requests since startup")
		out.Facts["activeRequests"] = measured(m.Inflight.Load(), "requests")
		out.Facts["httpBytesSent"] = measured(m.Sent.Load(), "bytes since startup")
		out.Facts["httpBytesReceived"] = measured(m.Received.Load(), "bytes since startup")
		if rateReady {
			out.Facts["bytesInPerSecond"] = Fact{State: "available", Value: inRate, Unit: "bytes/second", Reason: "Average since the previous console measurement."}
			out.Facts["bytesOutPerSecond"] = Fact{State: "available", Value: outRate, Unit: "bytes/second", Reason: "Average since the previous console measurement."}
		} else {
			out.Facts["bytesInPerSecond"] = unavailable("A second console measurement is needed to calculate an average.")
			out.Facts["bytesOutPerSecond"] = unavailable("A second console measurement is needed to calculate an average.")
		}
		if m.ActiveTranscodes == nil {
			out.Facts["activeTranscodes"] = unavailable("Playback conversion telemetry is not available.")
		} else if active, err := m.ActiveTranscodes(ctx); err != nil {
			out.Facts["activeTranscodes"] = unavailable("Playback conversion telemetry is temporarily unavailable.")
		} else {
			out.Facts["activeTranscodes"] = measured(active, "sessions")
		}
		out.Facts["remoteAccess"] = unavailable("Route and certificate observations are provided by the existing Networking settings, not inferred from HTTP counters.")
	default:
		return out, ErrInvalid
	}
	return out, ctx.Err()
}
