package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"time"
)

const (
	// Pressure checks only stat the WAL file. Thirty-second SQL polling allowed
	// ordinary scan batches to overshoot the hard allocation by hundreds of MiB.
	CheckpointPollInterval    = time.Second
	CheckpointRegularInterval = 30 * time.Second
	// CheckpointCopyInterval is how often new commits are copied into the
	// database by a PASSIVE checkpoint on a read connection. Writer automatic
	// checkpoints are disabled: page copying must not extend a committing
	// scan's write-gate hold by the time it takes to copy the whole backlog.
	CheckpointCopyInterval = 2 * time.Second
	checkpointPathDeadline = 5 * time.Second
)

// CheckpointSchedule belongs to one maintenance goroutine. Tick is shared by
// production housekeeping and capacity tests; it never starts a goroutine or
// adds filesystem work to a writer's critical path. The native filename stays
// private and is resolved once, rather than issuing a PRAGMA on every poll.
type CheckpointSchedule struct {
	db          *sql.DB
	walPath     string
	resolved    bool
	lastResolve time.Time
	lastAttempt time.Time
	nextAttempt time.Time
	settled     bool
	settledAt   uint64
	failures    int
}

// NewCheckpointSchedule always returns a usable schedule for a nonnil database.
// A bounded filename lookup failure is reported, while regular checkpoints and
// subsequent lookup retries remain available. Empty native filenames identify
// memory databases, which need no filesystem pressure probes.
func NewCheckpointSchedule(ctx context.Context, db *sql.DB) (*CheckpointSchedule, error) {
	if db == nil {
		return nil, errors.New("dbwork: nil checkpoint database")
	}
	s := &CheckpointSchedule{db: db, lastAttempt: time.Now()}
	return s, s.resolve(ctx)
}

func (s *CheckpointSchedule) resolve(ctx context.Context) error {
	s.lastResolve = time.Now()
	ctx, cancel := context.WithTimeout(ctx, checkpointPathDeadline)
	defer cancel()
	ctx = WithClass(ctx, ClassMaintenance)
	ctx = context.WithValue(ctx, checkpointReaderBypassKey{}, true)
	conn, err := ReadHandle(ctx, s.db).Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	path, err := checkpointDatabaseFile(ctx, conn)
	if err != nil {
		return err
	}
	if path != "" {
		s.walPath = path + "-wal"
	}
	s.resolved = true
	return nil
}

// Tick performs no SQL during ordinary one-second pressure polls. A checkpoint
// is due at hard physical allocation regardless of new commits, or on the
// regular interval when the previous result was unsettled or work changed.
// Failed pressure attempts honor both the reader backoff and a bounded local
// cooldown, including errors before the reader-pressure mechanism was reached.
func (s *CheckpointSchedule) Tick(ctx context.Context) (CheckpointResult, bool) {
	if ctx.Err() != nil {
		return CheckpointResult{}, false
	}
	now := time.Now()
	ledger := ledgerFor(s.db)
	ledger.probe()
	wakeWALWaiters()
	if !s.resolved && now.Sub(s.lastResolve) >= CheckpointRegularInterval {
		_ = s.resolve(ctx)
	}
	bytes, _ := checkpointFileBytes(s.walPath)
	soft, hard := ledger.pressure()
	pressure := bytes >= CheckpointHardFileBytes || hard || ledger.reservationPressure()
	commits := ChangingCommits()
	regular := now.Sub(s.lastAttempt) >= CheckpointRegularInterval && (!s.settled || commits != s.settledAt)
	copying := now.Sub(s.lastAttempt) >= CheckpointCopyInterval && commits != s.settledAt
	if (!pressure && !regular && !copying && !soft) || now.Before(s.nextAttempt) {
		return CheckpointResult{}, false
	}
	out := checkpoint(ctx, s.db, s.walPath)
	s.lastAttempt = time.Now()
	s.settledAt = commits
	after, known := checkpointFileBytes(s.walPath)
	s.settled = CheckpointSettled(out) && s.resolved && known && after < CheckpointHardFileBytes
	pressure = pressure || after >= CheckpointHardFileBytes || out.BeforeFileBytes >= CheckpointHardFileBytes
	cooldown := CheckpointPollInterval
	if pressure && !s.settled {
		s.failures = min(4, s.failures+1)
		cooldown = time.Second << (s.failures - 1)
	} else {
		s.failures = 0
	}
	if backoff := time.Duration(out.BackoffMs) * time.Millisecond; backoff > cooldown {
		cooldown = backoff
	}
	s.nextAttempt = s.lastAttempt.Add(cooldown)
	return out, true
}

func checkpointFileBytes(path string) (int64, bool) {
	if path == "" {
		return 0, true
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return 0, true
	}
	if err != nil {
		return 0, false
	}
	return info.Size(), true
}

// Fully copied PASSIVE frames alone cannot settle a deferred hard reset.
// Unknown future outcomes remain retryable rather than silently disabling care.
func CheckpointSettled(out CheckpointResult) bool {
	return out.Err == nil && out.Busy == 0 && out.Checkpoint == out.LogFrames && (out.Outcome == "copied" || out.Outcome == "truncated")
}
