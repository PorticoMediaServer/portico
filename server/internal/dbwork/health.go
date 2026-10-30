package dbwork

import (
	"context"
	"database/sql"
	"github.com/PorticoMediaServer/portico/server/internal/supervise"
	"sync"
	"time"
)

// Health separates a database that is genuinely unwell from one that is merely
// busy. Contention is normal and is handled by the gate and the retry budgets;
// a watchdog exists for the other case, where writes keep failing and the
// owner needs a prompt diagnostic. It does not pause background work.
const (
	// HealthProbeInterval is how often the watchdog asks the database a cheap
	// question.
	HealthProbeInterval = 5 * time.Second
	// HealthProbeDeadline bounds the probe itself. A probe that cannot answer in
	// this long is a failure, not a slow success.
	HealthProbeDeadline = 750 * time.Millisecond
	// HealthFailureThreshold and HealthRecoveryThreshold give the state machine
	// hysteresis, so one unlucky probe neither stops background work nor, once
	// stopped, restarts it prematurely.
	HealthFailureThreshold  = 2
	HealthRecoveryThreshold = 2
	// RecycleMinInterval rate-limits handle replacement. Recycling is a blunt
	// instrument; doing it in a loop would turn a bad minute into a bad hour.
	RecycleMinInterval = 30 * time.Second
)

// HealthState is what the watchdog currently believes.
type HealthState string

const (
	// HealthyState means background work may proceed.
	HealthyState HealthState = "healthy"
	// DegradedState means probes are failing; background work stands down.
	DegradedState HealthState = "degraded"
	// RecoveringState means probes have started succeeding again.
	RecoveringState HealthState = "recovering"
	// CorruptState means the database reported structural damage. Nothing
	// automated may attempt a repair: the only safe actions are to stop writing
	// and to tell the owner.
	CorruptState HealthState = "corrupt"
)

// HealthReport is the observable watchdog state.
type HealthReport struct {
	State            HealthState `json:"state"`
	Failures         int         `json:"consecutiveFailures"`
	Successes        int         `json:"consecutiveSuccesses"`
	LastError        string      `json:"lastError,omitempty"`
	LastProbeMillis  int64       `json:"lastProbeMillis"`
	Recycles         int         `json:"handleRecycles"`
	LastCheckedUnix  int64       `json:"lastCheckedUnix"`
	BackgroundPaused bool        `json:"backgroundPaused"`
}

// Watchdog probes one database handle on a timer.
type Watchdog struct {
	mu        sync.Mutex
	db        *sql.DB
	state     HealthState
	failures  int
	successes int
	lastError string
	lastProbe time.Duration
	lastCheck time.Time
	recycles  int
	recycled  time.Time

	// Reopen, when set, produces a fresh handle for the same database. The
	// watchdog calls it only for repeated busy or locked failures, never for
	// corruption.
	Reopen func() (*sql.DB, error)
	// Swap installs a replacement handle in whatever holds the live one.
	Swap func(*sql.DB)
}

// NewWatchdog returns a watchdog that reports healthy until told otherwise.
func NewWatchdog(db *sql.DB) *Watchdog {
	return &Watchdog{db: db, state: HealthyState}
}

// Run probes until ctx ends, and publishes the result as the background gate.
func (w *Watchdog) Run(ctx context.Context) {
	RegisterHealthProbe(w.AllowsBackground)
	defer RegisterHealthProbe(nil)
	ticker := time.NewTicker(HealthProbeInterval)
	defer ticker.Stop()
	w.Probe(ctx)
	deep := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Check the connection every five seconds, and execute SQL every
			// other check. Two SQL statements on every tick cost 24 idle
			// statements/minute even on a perfectly healthy server.
			w.probe(ctx, deep)
			deep = !deep
		}
	}
}

// Probe runs one health check and advances the state machine.
func (w *Watchdog) Probe(ctx context.Context) {
	w.probe(ctx, true)
}

func (w *Watchdog) probe(ctx context.Context, statement bool) {
	w.mu.Lock()
	db := w.db
	w.mu.Unlock()
	if db == nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, HealthProbeDeadline)
	start := time.Now()
	var err error
	if statement {
		var one int
		err = db.QueryRowContext(probeCtx, `SELECT 1`).Scan(&one)
	} else {
		err = db.PingContext(probeCtx)
	}
	cancel()
	elapsed := time.Since(start)
	if err == nil {
		err = WriteGate().StuckHolder(StuckWait)
	}

	w.mu.Lock()
	w.lastProbe = elapsed
	w.lastCheck = time.Now()
	if err == nil {
		w.failures = 0
		w.successes++
		w.lastError = ""
		switch w.state {
		case DegradedState:
			w.state = RecoveringState
		case RecoveringState:
			if w.successes >= HealthRecoveryThreshold {
				w.state = HealthyState
			}
		}
		w.mu.Unlock()
		return
	}
	w.successes = 0
	w.failures++
	w.lastError = err.Error()
	kind := Classify(err)
	if kind == KindCorrupt {
		// Corruption is never recovered from automatically. A rebuild here would
		// be a guess about which bytes are the good ones.
		w.state = CorruptState
		w.mu.Unlock()
		return
	}
	if w.failures >= HealthFailureThreshold && w.state != CorruptState {
		w.state = DegradedState
	}
	// A recycle is only safe when the rest of the process can be handed the new
	// handle. Without Swap every service still holds the old *sql.DB, and
	// closing it would wedge the whole server behind a watchdog that reports
	// healthy; degraded (which already stops background work) is the answer.
	shouldRecycle := w.mayRecycleLocked(kind)
	if shouldRecycle {
		w.recycled = time.Now()
	}
	w.mu.Unlock()
	if shouldRecycle {
		w.recycle(ctx)
	}
}

// mayRecycleLocked decides whether a failed probe may replace the handle. The
// caller holds w.mu.
func (w *Watchdog) mayRecycleLocked(kind Kind) bool {
	return w.state == DegradedState && (kind == KindBusy || kind == KindLocked) && w.Reopen != nil && w.Swap != nil && time.Since(w.recycled) >= RecycleMinInterval
}

// recycle replaces the handle after probing the replacement, so a broken new
// handle never displaces a working old one.
func (w *Watchdog) recycle(ctx context.Context) {
	replacement, err := w.Reopen()
	if err != nil || replacement == nil {
		return
	}
	probeCtx, cancel := context.WithTimeout(ctx, HealthProbeDeadline)
	var one int
	err = replacement.QueryRowContext(probeCtx, `SELECT 1`).Scan(&one)
	cancel()
	if err != nil {
		replacement.Close()
		return
	}
	w.mu.Lock()
	previous := w.db
	w.db = replacement
	w.recycles++
	w.mu.Unlock()
	if w.Swap != nil {
		w.Swap(replacement)
	}
	// Close the old handle asynchronously: in-flight statements on it must be
	// allowed to finish rather than be torn out from under their callers.
	supervise.Go("dbwork.watchdog.close-previous", func() {
		time.Sleep(time.Second)
		if previous != nil {
			previous.Close()
		}
	})
}

// AllowsBackground reports whether bulk work may proceed.
func (w *Watchdog) AllowsBackground() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.state == HealthyState || w.state == RecoveringState
}

// Report snapshots the watchdog.
func (w *Watchdog) Report() HealthReport {
	w.mu.Lock()
	defer w.mu.Unlock()
	checked := int64(0)
	if !w.lastCheck.IsZero() {
		checked = w.lastCheck.Unix()
	}
	return HealthReport{
		State:            w.state,
		Failures:         w.failures,
		Successes:        w.successes,
		LastError:        w.lastError,
		LastProbeMillis:  w.lastProbe.Milliseconds(),
		Recycles:         w.recycles,
		LastCheckedUnix:  checked,
		BackgroundPaused: !(w.state == HealthyState || w.state == RecoveringState),
	}
}
