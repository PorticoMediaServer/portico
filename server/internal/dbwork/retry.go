package dbwork

import (
	"context"
	"errors"
	"math/rand"
	"strings"
	"time"

	sqlite3 "modernc.org/sqlite/lib"
	"github.com/PorticoMediaServer/portico/server/internal/thirdparty/sqlite"
)

// Kind classifies a database failure by what the caller should do about it.
type Kind int

const (
	// KindOther is any failure with no special handling.
	KindOther Kind = iota
	// KindBusy is SQLITE_BUSY: another writer holds the lock.
	KindBusy
	// KindLocked is SQLITE_LOCKED: a table in this connection is locked.
	KindLocked
	// KindConstraint is a rejected write. Retrying cannot help.
	KindConstraint
	// KindCorrupt is structural damage. Nothing automated may try to repair it.
	KindCorrupt
	// KindFull is SQLITE_FULL: the volume holding the database has no room left.
	// Retrying cannot help and the caller is not at fault, so it is neither a
	// lock failure nor a constraint failure — and the owner is the only person
	// who can do anything about it, which is why it needs a code of its own
	// rather than an anonymous "the request could not be saved".
	KindFull
	// KindCapacity is a write the gate refused before it began because its
	// ticket pool was full of user writes. Nothing was written; the same
	// request can be retried later.
	KindCapacity
)

// Classify unwraps err to the driver error and maps its primary result code.
// Classification is typed first and only falls back to text matching, because a
// message match is a guess and a result code is the database's own answer.
func Classify(err error) Kind {
	if err == nil {
		return KindOther
	}
	if errors.Is(err, ErrWriterCapacity) {
		return KindCapacity
	}
	var typed *sqlite.Error
	if errors.As(err, &typed) {
		switch typed.Code() & 0xff {
		case sqlite3.SQLITE_BUSY:
			return KindBusy
		case sqlite3.SQLITE_LOCKED:
			return KindLocked
		case sqlite3.SQLITE_CONSTRAINT:
			return KindConstraint
		case sqlite3.SQLITE_CORRUPT, sqlite3.SQLITE_NOTADB:
			return KindCorrupt
		case sqlite3.SQLITE_FULL:
			return KindFull
		}
	}
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "database is locked"), strings.Contains(text, "sqlite_busy"):
		return KindBusy
	case strings.Contains(text, "database table is locked"), strings.Contains(text, "sqlite_locked"):
		return KindLocked
	case strings.Contains(text, "constraint failed"):
		return KindConstraint
	case strings.Contains(text, "malformed"), strings.Contains(text, "not a database"):
		return KindCorrupt
	case strings.Contains(text, "database or disk is full"), strings.Contains(text, "sqlite_full"):
		return KindFull
	}
	return KindOther
}

// ErrDatabaseFull is what a caller matches on when the volume is out of room.
// It is deliberately a sentinel rather than a kind test at every call site: the
// HTTP layer answers one typed 507, and everything else simply fails.
var ErrDatabaseFull = errors.New("the volume holding the database is full")

// Full reports whether err is SQLITE_FULL.
func Full(err error) bool { return Classify(err) == KindFull }

// Retryable reports whether err is lock contention that another attempt can win.
func Retryable(err error) bool {
	kind := Classify(err)
	return kind == KindBusy || kind == KindLocked
}

// Overloaded reports a write the writer could not take now and that changed
// nothing: a full ticket pool, or a lock error after admission. A request path
// answers it as a retryable refusal, never as a failed save.
func Overloaded(err error) bool {
	kind := Classify(err)
	return kind == KindCapacity || kind == KindBusy || kind == KindLocked
}

// Retry describes an attempt budget. Attempts counts the first try.
type Retry struct {
	Attempts int
	Base     time.Duration
	Max      time.Duration
}

// InstallRetry covers schema installation and startup pragmas, which run before
// normal service opens, so a retry there cannot stack behind the gate. Every
// gated write makes one attempt per admission: the gate resolves same-process
// contention before SQLite sees it, and a retry wrapped around an admission
// would multiply the gate wait and the connection busy timeout.
var InstallRetry = Retry{Attempts: 12, Base: 50 * time.Millisecond, Max: time.Second}

// Stats reports what a retried operation actually cost, so lock contention is
// measurable instead of merely survivable.
type Stats struct {
	Attempts int
	Retries  int
	Wait     time.Duration
}

// delay is exponential with a cap and 25% jitter. Jitter matters more than the
// curve: several writers that back off in lockstep collide again on every
// attempt, and a home server's contention is exactly that shape.
func (r Retry) delay(attempt int) time.Duration {
	base := r.Base
	if base <= 0 {
		base = 10 * time.Millisecond
	}
	shift := attempt
	if shift > 8 {
		shift = 8
	}
	wait := base << shift
	if r.Max > 0 && wait > r.Max {
		wait = r.Max
	}
	return wait + time.Duration(rand.Int63n(int64(wait/4)+1))
}

// Do runs attempt until it succeeds, fails for a non-lock reason, exhausts the
// budget, or ctx ends. ctx is checked before every attempt and during every
// sleep so a cancelled request stops immediately instead of spending the budget.
func (r Retry) Do(ctx context.Context, attempt func() error) (Stats, error) {
	attempts := r.Attempts
	if attempts < 1 {
		attempts = 1
	}
	var stats Stats
	var err error
	for index := 0; index < attempts; index++ {
		if ctxErr := ctx.Err(); ctxErr != nil {
			if err == nil {
				err = ctxErr
			}
			return stats, err
		}
		stats.Attempts++
		err = attempt()
		if err == nil || !Retryable(err) {
			return stats, err
		}
		if index == attempts-1 {
			break
		}
		wait := r.delay(index)
		stats.Retries++
		stats.Wait += wait
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return stats, ctx.Err()
		case <-timer.C:
		}
	}
	return stats, err
}
