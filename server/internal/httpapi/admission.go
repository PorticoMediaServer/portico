package httpapi

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/hostlimits"
)

// Reads do not queue at the write gate — in WAL a reader never blocks the
// writer — so the place to refuse more work than the server can carry is here,
// at the edge, where the answer can be an honest 503 with a Retry-After instead
// of a request that quietly takes a minute.
//
// A lane is a class of request that competes for one kind of scarce thing:
// catalogue reads compete for pooled connections, media bodies for disk and
// socket buffers, realtime streams for goroutines and memory. One global limit
// cannot express that, and a limit per route would be unmaintainable.

const (
	laneAuth         = "auth"
	laneBrowsing     = "browsing"
	laneExpensive    = "expensive"
	lanePlayback     = "playback"
	laneMedia        = "media"
	laneMediaBody    = "media-body"
	laneBulkTransfer = "bulk-transfer"
	laneRealtime     = "realtime"
	// laneObservation carries the 1 Hz playback observations. Its handler
	// touches only memory, so it has no queue: a tick that cannot start now is
	// replaced by the next one, never waited on.
	laneObservation   = "observation"
	laneAdmin         = "admin"
	laneAdminHeavy    = "admin-heavy"
	laneAdminLong     = "admin-long"
	laneUpload        = "upload"
	laneSecurityFence = "security-fence"
	lanePlaybackStop  = "playback-stop"
	laneDefault       = "default"
	// laneTools carries MCP requests. One holds its slot while the API calls
	// it makes are admitted in their own lanes, so it has a budget long enough
	// for the slowest of those and is kept apart from the lanes people use.
	laneTools = "tools"
)

// laneSpec is everything admission needs to know about a lane.
type laneSpec struct {
	// capacity is how many requests may be in flight at once.
	capacity int
	// queueCapacity and queuePerKey bound waiting requests, not active work.
	// Zero selects defaults of twice capacity globally and capacity per client.
	queueCapacity int
	queuePerKey   int
	// queueWait is how long a request waits for a slot before it is refused.
	// Zero means it is refused immediately rather than queued.
	queueWait time.Duration
	// budget is the per-request deadline. Zero means none, which is correct for
	// anything that legitimately runs as long as the client keeps reading.
	budget time.Duration
	// class is where this lane's database writes sit in the priority ladder.
	class dbwork.Class
	// pressure marks a lane whose activity means a person is waiting, which is
	// what background loops yield to.
	pressure bool
	// endsWithClient marks a lane whose only bound on a request's duration is
	// its client going away. A request that can no longer be cancelled is
	// refused there instead of being started.
	endsWithClient bool
}

// The capacities are sized against the connection pool and the target hardware:
// a home server carrying 100-200 viewers. Browsing is the one that matters most
// — it is deliberately several times the pool, because a browse request spends
// most of its life composing a response rather than holding a connection, and
// database/sql's own queue absorbs the difference. That queue is visible in
// diagnostics as WaitCount and WaitDuration; if those grow, this is the number
// to revisit, with evidence.
var laneSpecs = map[string]laneSpec{
	laneUpload:        {capacity: 2, queueWait: 0, budget: 0, class: dbwork.ClassInteractive, pressure: false, endsWithClient: true},
	lanePlaybackStop:  {capacity: 16, queueCapacity: 200, queuePerKey: 200, queueWait: 9 * time.Second, budget: 10 * time.Second, class: dbwork.ClassSecurityFence, pressure: true},
	laneSecurityFence: {capacity: 16, queueWait: 9 * time.Second, budget: 10 * time.Second, class: dbwork.ClassSecurityFence, pressure: true},
	laneAuth:          {capacity: 64, queueWait: 1500 * time.Millisecond, budget: 5 * time.Second, class: dbwork.ClassSecurityFence, pressure: true},
	laneBrowsing:      {capacity: 24, queueWait: 3 * time.Second, budget: 5 * time.Second, class: dbwork.ClassInteractive, pressure: true},
	laneExpensive:     {capacity: 8, queueWait: 3 * time.Second, budget: 5 * time.Second, class: dbwork.ClassInteractive, pressure: true},
	lanePlayback:      {capacity: 32, queueWait: 9 * time.Second, budget: 10 * time.Second, class: dbwork.ClassEstablishedPlayback, pressure: true},
	laneMedia:         {capacity: 100, queueWait: 1500 * time.Millisecond, budget: 5 * time.Second, class: dbwork.ClassInteractive, pressure: true},
	laneMediaBody:     {capacity: 256, queueWait: 0, budget: 0, class: dbwork.ClassEstablishedPlayback, pressure: false},
	laneBulkTransfer:  {capacity: 16, queueWait: 0, budget: 0, class: dbwork.ClassForegroundTransfer, pressure: false},
	laneRealtime:      {capacity: 512, queueWait: 0, budget: 0, class: dbwork.ClassInteractive, pressure: false},
	laneObservation:   {capacity: 512, queueWait: 0, budget: 1500 * time.Millisecond, class: dbwork.ClassEstablishedPlayback, pressure: false},
	laneAdmin:         {capacity: 100, queueWait: 1500 * time.Millisecond, budget: 5 * time.Second, class: dbwork.ClassInteractive, pressure: true},
	laneAdminHeavy:    {capacity: 4, queueWait: 1500 * time.Millisecond, budget: 10 * time.Second, class: dbwork.ClassMaintenance, pressure: false},
	// Owner operations that read or copy the whole database: staging a restore,
	// the on-demand integrity check and its repair. Their duration is the size of
	// the database, so a request budget would fail them on exactly the servers
	// that most need them. They are bounded by number instead, and their
	// database work ends when the owner's request goes away (a file copy
	// already under way finishes first).
	laneAdminLong: {capacity: 2, queueWait: 1500 * time.Millisecond, budget: 0, class: dbwork.ClassMaintenance, pressure: false, endsWithClient: true},
	laneTools:     {capacity: 16, queueWait: 1500 * time.Millisecond, budget: 30 * time.Second, class: dbwork.ClassInteractive, pressure: false},
	laneDefault:   {capacity: 100, queueWait: 1500 * time.Millisecond, budget: 5 * time.Second, class: dbwork.ClassInteractive, pressure: true},
}

// A lane is a counting semaphore plus the counters that make it explainable,
// and a per-client ledger so that the semaphore is shared rather than raced for.
// See fairness.go for why the ledger is there and when it engages.
type lane struct {
	name                    string
	spec                    laneSpec
	tokens                  chan struct{}
	active                  atomic.Int64
	admitted                atomic.Uint64
	queued                  atomic.Uint64
	rejected                atomic.Uint64
	noQueueRejected         atomic.Uint64
	queueFullRejected       atomic.Uint64
	clientQueueFullRejected atomic.Uint64
	queueTimeoutRejected    atomic.Uint64
	queueCancelledRejected  atomic.Uint64
	throttled               atomic.Uint64
	waitNanos               atomic.Uint64
	// imbalance counts releases that found the token channel full. That cannot
	// happen while every acquire is paired with exactly one release, so a
	// non-zero value is a bug in this file and nothing else: the lane would
	// permanently shrink, one token at a time, with no other symptom than a
	// server that slowly stops admitting anyone.
	imbalance atomic.Uint64

	mu   sync.Mutex
	held map[string]int
	// interest counts every client currently in this lane, waiting ones
	// included. Shares engage only when there is more than one, so a single
	// client on a quiet server is never held to a share of a lane nobody else
	// wants — which is the difference between fairness and a new limit.
	interest      map[string]int
	waiting       map[string]int
	waiters       int
	queueCapacity int
	queuePerKey   int
	// changed is closed and replaced on every release, which is how a request
	// waiting for its share to come free learns to look again without polling.
	changed chan struct{}
}

func newLane(name string, spec laneSpec) *lane {
	if spec.queueWait > 0 {
		if spec.queueCapacity <= 0 {
			spec.queueCapacity = spec.capacity * 2
		}
		if spec.queuePerKey <= 0 {
			spec.queuePerKey = spec.capacity
		}
	}
	l := &lane{name: name, spec: spec, tokens: make(chan struct{}, spec.capacity), held: map[string]int{}, interest: map[string]int{}, waiting: map[string]int{}, changed: make(chan struct{}), queueCapacity: spec.queueCapacity, queuePerKey: spec.queuePerKey}
	for i := 0; i < spec.capacity; i++ {
		l.tokens <- struct{}{}
	}
	return l
}

// tryAcquire takes a slot for key without waiting. It reports whether it was
// admitted and, if not, whether the refusal was the client's own share rather
// than the lane being full — which is the difference between "the server is
// busy" and "you are asking for more than your share of it".
func (l *lane) tryAcquire(key string) (admitted, overShare bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.tryAcquireLocked(key, len(l.interest) > 1)
}

func (l *lane) tryAcquireLocked(key string, multipleClients bool) (admitted, overShare bool) {
	if multipleClients && l.active.Load()*100 >= int64(l.spec.capacity*fairnessEngagesAbove) && l.held[key] >= l.shareLimit() {
		l.throttled.Add(1)
		return false, true
	}
	select {
	case <-l.tokens:
	default:
		return false, false
	}
	l.held[key]++
	l.active.Add(1)
	l.admitted.Add(1)
	return true, false
}

// begin admits immediately or reserves a bounded queue place atomically. A
// refused request creates neither an interest entry nor a deadline timer.
func (l *lane) begin(key string) (admitted, waiting bool) {
	return l.beginWithQueueLimit(key, l.spec.queueCapacity, l.spec.queuePerKey, true)
}

// The Stop lane may reserve one wait slot per live session above its floor.
// Other lanes always use their fixed declarations.
func (l *lane) beginWithQueueLimit(key string, capacity, perKey int, recordRefusal bool) (admitted, waiting bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.queueCapacity, l.queuePerKey = capacity, perKey
	multiple := len(l.interest) > 1 || (len(l.interest) > 0 && l.interest[key] == 0)
	if admitted, _ := l.tryAcquireLocked(key, multiple); admitted {
		l.interest[key]++
		return true, false
	}
	if l.spec.queueWait <= 0 {
		l.noQueueRejected.Add(1)
		return false, false
	}
	if l.waiters >= capacity {
		if recordRefusal {
			l.queueFullRejected.Add(1)
		}
		return false, false
	}
	if l.waiting[key] >= perKey {
		if recordRefusal {
			l.clientQueueFullRejected.Add(1)
		}
		return false, false
	}
	l.waiters++
	l.waiting[key]++
	l.interest[key]++
	return false, true
}

func (l *lane) leaveQueue(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.waiters--
	if l.waiting[key] <= 1 {
		delete(l.waiting, key)
	} else {
		l.waiting[key]--
	}
}

// acquire waits for a slot for key until ctx is done. The wait is on the lane's
// broadcast rather than on the token channel, so a request that is over its
// share does not sit at the head of the queue holding a slot away from the
// clients whose share is free.
func (l *lane) acquire(ctx context.Context, key string) bool {
	for {
		if ctx.Err() != nil {
			return false
		}
		// Captured before the attempt, so a release between the attempt and the
		// select cannot be missed.
		l.mu.Lock()
		wait := l.changed
		l.mu.Unlock()
		if admitted, _ := l.tryAcquire(key); admitted {
			return true
		}
		select {
		case <-wait:
		case <-ctx.Done():
			return false
		}
	}
}

// enter and leave bracket one client's whole attempt on this lane, waiting
// included, so that the share rule can tell "one client wants this lane" from
// "several clients want this lane".
func (l *lane) enter(key string) {
	l.mu.Lock()
	l.interest[key]++
	l.mu.Unlock()
}

func (l *lane) leave(key string) {
	l.mu.Lock()
	if l.interest[key] <= 1 {
		delete(l.interest, key)
	} else {
		l.interest[key]--
	}
	l.mu.Unlock()
}

func (l *lane) release(key string) {
	l.mu.Lock()
	if l.held[key] <= 1 {
		delete(l.held, key)
	} else {
		l.held[key]--
	}
	l.active.Add(-1)
	select {
	case l.tokens <- struct{}{}:
	default:
		// Never reachable with balanced acquire/release; counted rather than
		// dropped so that if it ever happens it is visible in diagnostics and in
		// the load tier's invariants instead of showing up months later as a lane
		// that mysteriously admits fewer requests than its capacity.
		l.imbalance.Add(1)
	}
	woken := l.changed
	l.changed = make(chan struct{})
	l.mu.Unlock()
	close(woken)
}

// busy reports whether anything is in flight in this lane. It is the whole
// foreground-pressure signal: reading an atomic counter costs nothing, which is
// what lets a bulk loop consult it at every batch boundary. The earlier design
// rebuilt full resource diagnostics for this, which meant the scanner queried
// the database to find out how busy the database was.
func (l *lane) busy() bool { return l.active.Load() > 0 }

// LaneDiagnostics is one lane's observable state.
type LaneDiagnostics struct {
	Lane                   string `json:"lane"`
	Active                 int64  `json:"active"`
	Capacity               int    `json:"capacity"`
	Admitted               uint64 `json:"admitted"`
	Queued                 uint64 `json:"queued"`
	Waiting                int    `json:"waiting"`
	QueueCapacity          int    `json:"queueCapacity"`
	PerClientQueueCapacity int    `json:"perClientQueueCapacity"`
	Rejected               uint64 `json:"rejected"`
	// Refusal reasons partition completed rejected attempts. Full-lane queue
	// classification precedes the client bound; an expired request context is
	// cancellation, while the lane's own queueWait expiry is a queue timeout.
	NoQueueRefusals           uint64 `json:"noQueueRefusals"`
	QueueFullRefusals         uint64 `json:"queueFullRefusals"`
	ClientQueueFullRefusals   uint64 `json:"clientQueueFullRefusals"`
	QueueTimeoutRefusals      uint64 `json:"queueTimeoutRefusals"`
	QueueCancellationRefusals uint64 `json:"queueCancellationRefusals"`
	// ShareLimit is how many of this lane's slots one client may hold once the
	// lane is contended, and Throttled how many times a client was held to it.
	// Throttled is not a refusal: almost all of them are admitted a moment later.
	ShareLimit int    `json:"perClientShare"`
	Throttled  uint64 `json:"perClientThrottled"`
	// Imbalance must be zero. See lane.release.
	Imbalance    uint64 `json:"releaseImbalance"`
	QueueWaitMs  uint64 `json:"queueWaitMillis"`
	BudgetMillis int64  `json:"requestBudgetMillis"`
	QueueLimitMs int64  `json:"queueLimitMillis"`
	WorkClass    string `json:"workClass"`
	CountsAsLoad bool   `json:"countsAsForegroundLoad"`
}

// admission owns the lanes for one server. It is created once, with the router.
type admission struct {
	lanes   map[string]*lane
	order   []string
	clients *fairnessIdentities
	// trustedProxies is how a client address is resolved for the share key of an
	// unauthenticated request; it is the router's own list, not a second one.
	trustedProxies []netip.Prefix

	livePlaybackSessions func(context.Context) (int, error)
	searchMu             sync.Mutex
	searchPerKey         map[string]int
	searchTotal          int
	searchDenied         atomic.Uint64
}

// Search is the one read that can cost more than the rest of a page put
// together, so it has a second limit on top of its lane: a per-viewer cap so one
// client's retry loop cannot consume the whole expensive lane, and a global cap.
const (
	maxSearchesPerViewer = 2
	maxSearchesGlobal    = 100
)

func newAdmission() *admission {
	return newAdmissionForMemory(hostlimits.EffectiveMemoryBytes())
}

func newAdmissionForMemory(memoryBytes int64) *admission {
	a := &admission{lanes: map[string]*lane{}, searchPerKey: map[string]int{}, clients: newFairnessIdentities()}
	for name, spec := range laneSpecs {
		if name == laneBrowsing {
			spec.queueCapacity = browsingQueueCapacity(memoryBytes)
		}
		a.lanes[name] = newLane(name, spec)
		a.order = append(a.order, name)
	}
	sortStrings(a.order)
	return a
}

func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}

// registerPressure publishes this server's foreground signal to the background
// loops, so a scan yields while a person is browsing and resumes when they stop.
func (a *admission) registerPressure() {
	// One process serves one database, so the signal is registered by source
	// name: a handler built later replaces this one's probes rather than adding
	// a second set that would keep reporting an idle server as busy.
	probes := []func() bool{}
	for _, name := range a.order {
		l := a.lanes[name]
		if l.spec.pressure {
			probes = append(probes, l.busy)
		}
	}
	dbwork.RegisterForegroundProbe("httpapi", func() bool {
		for _, probe := range probes {
			if probe() {
				return true
			}
		}
		return false
	})
}

// acquireSearch applies the per-viewer and global search caps.
func (a *admission) acquireSearch(key string) (func(), bool) {
	a.searchMu.Lock()
	defer a.searchMu.Unlock()
	if a.searchTotal >= maxSearchesGlobal || a.searchPerKey[key] >= maxSearchesPerViewer {
		a.searchDenied.Add(1)
		return nil, false
	}
	a.searchTotal++
	a.searchPerKey[key]++
	var once sync.Once
	return func() {
		once.Do(func() {
			a.searchMu.Lock()
			defer a.searchMu.Unlock()
			a.searchTotal--
			if a.searchPerKey[key] <= 1 {
				delete(a.searchPerKey, key)
			} else {
				a.searchPerKey[key]--
			}
		})
	}, true
}

func (a *admission) diagnostics() []LaneDiagnostics {
	out := []LaneDiagnostics{}
	for _, name := range a.order {
		l := a.lanes[name]
		l.mu.Lock()
		waiting := l.waiters
		queueCapacity, queuePerKey := l.queueCapacity, l.queuePerKey
		l.mu.Unlock()
		out = append(out, LaneDiagnostics{
			Lane:                      name,
			Active:                    l.active.Load(),
			Capacity:                  l.spec.capacity,
			Admitted:                  l.admitted.Load(),
			Queued:                    l.queued.Load(),
			Waiting:                   waiting,
			QueueCapacity:             queueCapacity,
			PerClientQueueCapacity:    queuePerKey,
			Rejected:                  l.rejected.Load(),
			NoQueueRefusals:           l.noQueueRejected.Load(),
			QueueFullRefusals:         l.queueFullRejected.Load(),
			ClientQueueFullRefusals:   l.clientQueueFullRejected.Load(),
			QueueTimeoutRefusals:      l.queueTimeoutRejected.Load(),
			QueueCancellationRefusals: l.queueCancelledRejected.Load(),
			QueueWaitMs:               l.waitNanos.Load() / uint64(time.Millisecond),
			ShareLimit:                l.shareLimit(),
			Throttled:                 l.throttled.Load(),
			Imbalance:                 l.imbalance.Load(),
			BudgetMillis:              l.spec.budget.Milliseconds(),
			QueueLimitMs:              l.spec.queueWait.Milliseconds(),
			WorkClass:                 l.spec.class.String(),
			CountsAsLoad:              l.spec.pressure,
		})
	}
	return out
}

// wrap is the admission middleware. It resolves the lane from the matched route,
// takes a slot or refuses honestly, applies the lane's request budget, and puts
// the lane's work class in the context so every write the handler makes lands in
// the right place in the priority ladder.
func (a *admission) wrap(mux *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pattern, name := a.classify(mux, r)
		l := a.lanes[name]
		if l == nil {
			l = a.lanes[laneDefault]
		}
		// A request served on a canceled connection has had its cancellation
		// detached, on the understanding that the lane's budget bounds it. Here
		// there is no budget, so nothing would end the work if the owner left.
		// The refusal is retryable and the connection closes with it, so the
		// retry arrives on a fresh connection that can be watched.
		if l.spec.endsWithClient && cancellationDetached(r.Context()) {
			a.refuse(w, r, l)
			return
		}
		ctx := r.Context()
		// Search and Stop share their total budget between queueing and work. A
		// legitimate burst may wait as long as browsing, but admission cannot
		// renew the deadline before the handler installs its narrower budget.
		budgetIncludesQueue := (l.name == laneExpensive || l.name == lanePlaybackStop) && l.spec.budget > 0
		if budgetIncludesQueue {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, l.spec.budget)
			defer cancel()
		}
		// The share key is resolved before the lane is entered, because the whole
		// point is to decide admission without doing any work first.
		key := a.clientKey(r)
		var admitted, queued bool
		if l.name == lanePlaybackStop {
			admitted, queued = a.beginStop(ctx, l, key)
		} else {
			admitted, queued = l.begin(key)
		}
		if !admitted && !queued {
			a.refuse(w, r, l)
			return
		}
		defer l.leave(key)
		if queued {
			start := time.Now()
			l.queued.Add(1)
			queueCtx, cancel := context.WithTimeout(ctx, l.spec.queueWait)
			admitted := l.acquire(queueCtx, key)
			// Observe the cause before our cleanup cancel changes queueCtx.Err.
			queueErr := queueCtx.Err()
			cancel()
			l.leaveQueue(key)
			l.waitNanos.Add(uint64(time.Since(start)))
			if !admitted {
				if r.Context().Err() != nil || queueErr != context.DeadlineExceeded {
					l.queueCancelledRejected.Add(1)
				} else {
					l.queueTimeoutRejected.Add(1)
				}
				a.refuse(w, r, l)
				return
			}
		}
		defer l.release(key)
		if budgetIncludesQueue && ctx.Err() != nil {
			a.refuse(w, r, l)
			return
		}
		ctx = dbwork.WithClass(ctx, l.spec.class)
		// The writer gate rotates between the same owners the lanes share load
		// by, and a full gate refuses an over-share owner's write first.
		ctx = dbwork.WithOwner(ctx, key)
		if l.spec.budget > 0 && !budgetIncludesQueue {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, l.spec.budget)
			defer cancel()
		}
		// Every request carries its own statement counter. The driver fills it,
		// so the number covers work the handler did not know it was asking for —
		// a trigger cascade, a view that expands into nine branches, a policy
		// check inside a loop.
		ctx, cost := dbwork.Measure(ctx)
		defer func() { recordRouteCost(pattern, l.name, cost()) }()
		if !uncompressedLanes[l.name] {
			response := withResponseDeadline(w)
			defer response.finishResponse()
			w = response
		}
		// Compression is decided by lane, so it stays in step with this table: a
		// route classified as a media body is one, whatever it is called, and a
		// realtime stream is never buffered into a compressor.
		if !uncompressedLanes[l.name] && acceptsGzip(r) {
			compressed := &compressingWriter{ResponseWriter: w}
			defer compressed.close()
			w = compressed
		}
		if l.name == laneUpload {
			body := &progressUploadBody{ReadCloser: r.Body, controller: http.NewResponseController(w), idle: 60 * time.Second}
			r.Body = body
			defer body.clearDeadline()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (a *admission) refuse(w http.ResponseWriter, r *http.Request, l *lane) {
	l.rejected.Add(1)
	// An early HTTP/1 rejection must not drain a body the peer never sends.
	closeUnreadUpload(w, r)
	// A client that stops reading must not turn a fast overload refusal into
	// an unbounded response goroutine. This deadline covers this small answer
	// only; long-lived media and event streams keep their own rolling budgets.
	controller := http.NewResponseController(w)
	bounded := controller.SetWriteDeadline(time.Now().Add(2*time.Second)) == nil
	if bounded {
		defer controller.SetWriteDeadline(time.Time{})
	}
	w.Header().Set("Retry-After", "1")
	write(w, 503, map[string]any{"error": map[string]any{"code": "server_busy", "message": "The server is busy handling other requests. Try again shortly.", "retryable": true}})
	if bounded {
		// Small JSON bodies are buffered. Flush while the deadline is armed,
		// rather than leaving the server's final flush outside this budget.
		_ = controller.Flush()
	}
}

// classify resolves a request to the route that will actually serve it and the
// lane that route belongs to. http.ServeMux already knows which pattern matches;
// asking it is both exact and free of the prefix-matching mistakes a second,
// parallel path table would make. The pattern is also the low-cardinality label
// the per-request cost table is keyed on.
func (a *admission) classify(mux *http.ServeMux, r *http.Request) (string, string) {
	_, pattern := mux.Handler(r)
	if pattern == "" {
		return "", laneDefault
	}
	if lane, ok := routeLanes[pattern]; ok {
		return pattern, lane
	}
	// A pattern the table does not name is classified by its method and its
	// first path segment, which is a far narrower guess than a prefix scan.
	return pattern, laneForUnlistedPattern(pattern)
}

// laneFor is classify's lane half, for callers that do not need the pattern.
func (a *admission) laneFor(mux *http.ServeMux, r *http.Request) string {
	_, lane := a.classify(mux, r)
	return lane
}

func laneForUnlistedPattern(pattern string) string {
	method, path := "", pattern
	if index := strings.Index(pattern, " "); index > 0 {
		method, path = pattern[:index], pattern[index+1:]
	}
	segments := strings.Split(strings.TrimPrefix(path, "/"), "/")
	head := ""
	if len(segments) > 1 {
		head = segments[1]
	}
	switch head {
	case "sessions", "setup", "auth", "direct":
		return laneAuth
	case "media", "hls", "segments":
		return laneMediaBody
	case "playback-sessions", "playback", "queues", "control":
		return lanePlayback
	case "admin", "console", "operations", "diagnostics":
		if method == "GET" {
			return laneAdmin
		}
		return laneAdminHeavy
	case "items", "libraries", "browse", "home", "people", "collections":
		return laneBrowsing
	case "search":
		return laneExpensive
	case "events", "notifications":
		return laneRealtime
	case "downloads":
		return laneBulkTransfer
	}
	return laneDefault
}
