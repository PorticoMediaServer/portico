package dbwork

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// A read snapshot pins one WAL frame boundary: while it is open a checkpoint
// cannot reset the log past it. So every snapshot this package opens lives at
// most min(caller deadline, now+2 s foreground / now+500 ms background)
// (F §7), never the caller's deadline alone, and a nested snapshot joins its
// parent without renewing it. A composite response gets one whole snapshot
// within that bound; one that runs out fails as a timeout, never a partial
// page.
//
// The one exception is the owner-long read: an enumerated whole-database
// operation (integrity check, repair, restore staging) the owner asked for.
// It takes the single long-read permit and may hold a background snapshot for
// IntegrityCheckDeadline.
const (
	ForegroundSnapshotLifetime = 2 * time.Second
	BackgroundSnapshotLifetime = 500 * time.Millisecond
	LongReadLifetime           = IntegrityCheckDeadline
)

// LongRead names an operation allowed the owner-long read permit.
type LongRead string

const (
	LongReadIntegrity      LongRead = "integrity"
	LongReadRepair         LongRead = "repair"
	LongReadRestoreStaging LongRead = "restore-staging"
)

// ErrLongReadBusy reports that another owner-long read holds the permit.
var ErrLongReadBusy = errors.New("dbwork: another whole-database read is running")

var longReadPermit = make(chan struct{}, 1)

// longReadHolder cancels the permit holder. A ten-minute snapshot pins the
// WAL, so a hard-pressure recovery ends it with ErrReadPressure (a clean,
// retryable refusal) rather than letting the log grow without limit.
var longReadHolder struct {
	sync.Mutex
	cancel context.CancelCauseFunc
}

// cancelLongRead ends the owner-long read in progress, if any, with
// ErrReadPressure, and reports whether there was one.
func cancelLongRead() bool {
	longReadHolder.Lock()
	defer longReadHolder.Unlock()
	if longReadHolder.cancel == nil {
		return false
	}
	longReadHolder.cancel(ErrReadPressure)
	return true
}

type longReadKey struct{}

// AcquireLongRead takes the single owner-long read permit for op. The returned
// context's snapshots run on the background pool and may live up to
// LongReadLifetime; release frees the permit. Only the enumerated operations
// qualify, and a second concurrent request is refused, never queued.
func AcquireLongRead(ctx context.Context, op LongRead) (context.Context, func(), error) {
	switch op {
	case LongReadIntegrity, LongReadRepair, LongReadRestoreStaging:
	default:
		return nil, nil, fmt.Errorf("dbwork: %q is not a whole-database read", op)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case longReadPermit <- struct{}{}:
	default:
		return nil, nil, ErrLongReadBusy
	}
	ctx, cancel := context.WithCancelCause(ctx)
	longReadHolder.Lock()
	longReadHolder.cancel = cancel
	longReadHolder.Unlock()
	var released atomic.Bool
	release := func() {
		if released.CompareAndSwap(false, true) {
			longReadHolder.Lock()
			longReadHolder.cancel = nil
			longReadHolder.Unlock()
			cancel(nil)
			<-longReadPermit
		}
	}
	return context.WithValue(WithClass(ctx, ClassMaintenance), longReadKey{}, op), release, nil
}

func longRead(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	_, ok := ctx.Value(longReadKey{}).(LongRead)
	return ok
}

// snapshotLimit is how long a snapshot opened under ctx may live.
func snapshotLimit(ctx context.Context) time.Duration {
	if longRead(ctx) {
		return LongReadLifetime
	}
	if ClassFrom(ctx, ClassInteractive).Background() {
		return BackgroundSnapshotLifetime * snapshotScale
	}
	return ForegroundSnapshotLifetime * snapshotScale
}

// snapshotContext applies the lifetime cap to ctx. It keeps an earlier caller
// deadline and otherwise sets the cap; the cancel is always safe to call.
func snapshotContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	deadline := time.Now().Add(snapshotLimit(ctx))
	if current, ok := ctx.Deadline(); ok && current.Before(deadline) {
		return ctx, func() {}
	}
	return context.WithDeadline(ctx, deadline)
}

var overLifetime struct {
	foreground, background atomic.Uint64
}

// noteReaderLifetime counts a read lease that outlived its class's snapshot
// lifetime: an implicit Rows left open, a raw BeginTx outside these helpers,
// or a snapshot whose cap was not applied. They are counted (and reported by a
// census run), not interrupted.
func noteReaderLifetime(lease *readerLease, elapsed time.Duration) {
	if lease.long {
		return
	}
	limit := ForegroundSnapshotLifetime * snapshotScale
	counter := &overLifetime.foreground
	if lease.class.Background() {
		limit, counter = BackgroundSnapshotLifetime*snapshotScale, &overLifetime.background
	}
	if elapsed <= limit {
		return
	}
	counter.Add(1)
	if !testing.Testing() || lease.site.n == 0 {
		return
	}
	function, file, line, inTest := lease.site.resolve()
	if inTest {
		return
	}
	if path := os.Getenv(PlanReportEnv); path != "" {
		planReport.Lock()
		if f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
			fmt.Fprintf(f, "dbwork: a %s read in %s held its snapshot %s (limit %s)\t%s:%d\t%s\n", lease.class, function, elapsed.Round(time.Millisecond), limit, file, line, lease.site.chain())
			f.Close()
		}
		planReport.Unlock()
	}
}

// ReadersOverLifetime reports leases that outlived their snapshot lifetime.
func ReadersOverLifetime() (foreground, background uint64) {
	return overLifetime.foreground.Load(), overLifetime.background.Load()
}
