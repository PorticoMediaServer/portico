package dbwork

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// The write-ahead log is the one resource in this design with no natural ceiling
// and no observability. `wal_autocheckpoint=1000` (≈4 MiB) only runs a PASSIVE
// checkpoint, which copies frames through the oldest reader's end mark but
// cannot reset the log while that reader still needs it. A large library scan
// writing continuously while two hundred viewers hold browse snapshots is
// exactly that shape, and the failure mode — a `-wal` file growing into the tens
// of gigabytes on the same volume as the generated media — is invisible in every
// diagnostic the server publishes.
//
// Measuring it is read-only and costs nothing, so it comes first.

// WALStats is the write-ahead log's observable state. Busy, LogFrames and
// CheckpointedFrames are the three values `PRAGMA wal_checkpoint` itself returns;
// FileBytes is the `-wal` file on disk, which is the number that fills a volume.
type WALStats struct {
	Busy               int                 `json:"busy"`
	LogFrames          int                 `json:"logFrames"`
	CheckpointedFrames int                 `json:"checkpointedFrames"`
	BacklogFrames      int                 `json:"backlogFrames"`
	FileBytes          int64               `json:"walFileBytes"`
	PeakFileBytes      int64               `json:"walPeakFileBytes"`
	LastError          string              `json:"lastError,omitempty"`
	Readers            ReaderLifetimeStats `json:"readers"`
	Checkpoints        CheckpointStats     `json:"checkpoints"`
}

// walPeak remembers the largest `-wal` ever observed in this process's life. A
// current size of zero says nothing about whether the log spiked to a gigabyte
// an hour ago, and the soak tier's pass criterion is about the peak.
var walPeak atomic.Int64

// WAL reports the checkpoint owner's last result and the current retained file
// allocation. It never initiates copying or competes with the checkpoint owner.
// The logical counters can therefore be older than FileBytes.
func WAL(ctx context.Context, db *sql.DB) WALStats {
	out := WALStats{}
	if db == nil {
		return out
	}
	// Diagnostics read the owner's last result; they never initiate copying.
	if l := ledgerFor(db); l != nil {
		l.mu.Lock()
		last, observed := l.lastCheckpoint, l.checkpointKnown
		l.mu.Unlock()
		if observed {
			out.Busy, out.LogFrames, out.CheckpointedFrames = last.Busy, last.LogFrames, last.Checkpoint
			if last.Err != nil {
				out.LastError = last.Err.Error()
			}
		} else {
			out.LastError = "checkpoint owner has not sampled the WAL"
		}
	} else {
		out.LastError = "checkpoint owner unavailable"
	}
	if l := ledgerFor(db); l != nil {
		var known bool
		out.FileBytes, known = checkpointFileBytes(l.path + "-wal")
		if !known {
			out.LastError = "WAL allocation unavailable"
		}
	} else {
		out.FileBytes = walFileBytes(ctx, db)
	}
	out.BacklogFrames = max(0, out.LogFrames-out.CheckpointedFrames)
	for {
		peak := walPeak.Load()
		if out.FileBytes <= peak || walPeak.CompareAndSwap(peak, out.FileBytes) {
			break
		}
	}
	out.PeakFileBytes = walPeak.Load()
	out.Readers = ReaderLifetimes()
	out.Checkpoints = Checkpoints()
	return out
}

// walFileBytes sizes the `-wal` beside the main database. The path comes from
// the database itself rather than from a value threaded through forty
// constructors: `PRAGMA database_list` is the handle's own answer to "which file
// am I?" and stays correct across a recycled handle.
func walFileBytes(ctx context.Context, db *sql.DB) int64 {
	path := DatabaseFile(ctx, db)
	if path == "" {
		return 0
	}
	info, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}

// DatabaseFile returns the main database's path, or "" for a memory database.
func DatabaseFile(ctx context.Context, db *sql.DB) string {
	if db == nil {
		return ""
	}
	rows, err := db.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return ""
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if err = rows.Scan(&seq, &name, &file); err != nil {
			return ""
		}
		if name == "main" {
			return file
		}
	}
	return ""
}

// checkpointDatabaseFile asks the connection already reserved for maintenance,
// never another pool slot. SQLite supplies the native filename, including any
// symlink resolution. A memory database reports an empty filename.
func checkpointDatabaseFile(ctx context.Context, conn *sql.Conn) (file string, err error) {
	rows, err := conn.QueryContext(ctx, `PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	for rows.Next() {
		var seq int
		var name, candidate string
		if err = rows.Scan(&seq, &name, &candidate); err != nil {
			return "", err
		}
		if name == "main" {
			return candidate, nil
		}
	}
	return "", rows.Err()
}

// One checkpoint ran in the whole life of the process: a PASSIVE pass at
// startup. `wal_autocheckpoint=1000` covers roughly four mebibytes, and a PASSIVE
// checkpoint cannot reset the log while a reader needs older frames. Overlapping
// browse snapshots can retain that boundary continuously. The log grows onto
// the same volume as the generated
// media until the disk fills and SQLite starts answering SQLITE_FULL.
//
// So there is a recurring checkpoint, in the maintenance class, and it escalates:
// PASSIVE while the log is small, TRUNCATE once the log is past a threshold AND
// nobody is writing or waiting to write, or at the hard threshold. A short
// SQLite busy budget bounds waiting for readers; the shared gate preserves
// writer priority. Page copying and filesystem sync still depend on storage.

const (
	// CheckpointTruncateFrames is the log length past which a PASSIVE pass is
	// clearly not keeping up and a TRUNCATE is worth its cost. Roughly 64 MiB at
	// the 4 KiB page size this policy uses.
	CheckpointTruncateFrames = 16000
	// CheckpointHardFrames triggers a bounded drain of overlapping readers even
	// while foreground browsing continues. Roughly 192 MiB. Copying stays outside
	// the writer gate, and a reset still requires an unchanged copied log.
	CheckpointHardFrames = 48000
	// CheckpointHardFileBytes covers a large retained allocation even after a
	// writer resets and reuses it with only a handful of current logical frames.
	// PASSIVE copying alone cannot release those bytes back to the filesystem.
	CheckpointHardFileBytes = 192 << 20
	// CheckpointDeadline bounds connection/gate waiting and supplies query
	// cancellation. It is not a guaranteed wall-clock cap for SQLite's WAL
	// copying, fsync or busy callback; the lock wait has a separate short budget.
	CheckpointDeadline = 20 * time.Second
	// SQLite's WAL busy callback does not observe context interruption. Bound
	// its wait separately so a pinned reader cannot monopolize the write gate.
	CheckpointBusyTimeout     = 50 * time.Millisecond
	checkpointCleanupDeadline = time.Second
)

// CheckpointResult says what one maintenance checkpoint did.
type CheckpointResult struct {
	Mode                     string              `json:"mode"`
	Busy                     int                 `json:"busy"`
	LogFrames                int                 `json:"logFrames"`
	Checkpoint               int                 `json:"checkpointedFrames"`
	BeforeLogFrames          int                 `json:"beforeLogFrames"`
	BeforeCheckpointedFrames int                 `json:"beforeCheckpointedFrames"`
	BeforeFileBytes          int64               `json:"beforeFileBytes"`
	DurationMs               int64               `json:"durationMs"`
	DrainMs                  int64               `json:"drainMs"`
	Outcome                  string              `json:"outcome"`
	BackoffMs                int64               `json:"backoffMs"`
	Readers                  ReaderLifetimeStats `json:"readers"`
	Err                      error               `json:"-"`
}

// CheckpointStats retains fixed counts and the last outcome; SQL, arguments,
// database paths and error messages never enter this diagnostic state.
type CheckpointStats struct {
	Attempts         uint64           `json:"attempts"`
	Truncates        uint64           `json:"truncates"`
	Busy             uint64           `json:"busy"`
	Errors           uint64           `json:"errors"`
	PressureTimeouts uint64           `json:"pressureTimeouts"`
	MaxDurationMs    int64            `json:"maxDurationMs"`
	Last             CheckpointResult `json:"last"`
}

var checkpointObservation struct {
	sync.Mutex
	stats CheckpointStats
}

func Checkpoints() CheckpointStats {
	checkpointObservation.Lock()
	defer checkpointObservation.Unlock()
	return checkpointObservation.stats
}

func observeCheckpoint(out CheckpointResult) {
	checkpointObservation.Lock()
	defer checkpointObservation.Unlock()
	s := &checkpointObservation.stats
	s.Attempts++
	if out.Mode == "truncate" {
		s.Truncates++
	}
	if out.Busy != 0 {
		s.Busy++
	}
	if out.Err != nil {
		s.Errors++
	}
	if out.Outcome == "reader-timeout" {
		s.PressureTimeouts++
	}
	s.MaxDurationMs = max(s.MaxDurationMs, out.DurationMs)
	out.Err = nil
	s.Last = out
}

// Checkpoint runs one maintenance checkpoint, escalating to TRUNCATE when the
// log has grown past the threshold and the write gate is idle.
func Checkpoint(ctx context.Context, db *sql.DB) CheckpointResult {
	return checkpoint(ctx, db, "")
}

// knownWALPath is the full native main-database filename plus "-wal". The
// recurring controller resolves it once; direct callers discover it on the
// connection this checkpoint already holds. Paths never enter diagnostic state.
func checkpoint(ctx context.Context, db *sql.DB, knownWALPath string) (out CheckpointResult) {
	out = CheckpointResult{Mode: "passive"}
	if db == nil {
		return out
	}
	start := time.Now()
	defer func() {
		out.DurationMs = time.Since(start).Milliseconds()
		if out.Err != nil {
			out.Outcome = "error"
		} else if out.Outcome == "" {
			if out.Busy != 0 {
				out.Outcome = "busy"
			} else if out.LogFrames != out.Checkpoint {
				out.Outcome = "partial"
			} else if out.Mode == "truncate" {
				out.Outcome = "truncated"
			} else {
				out.Outcome = "copied"
			}
		}
		observeCheckpoint(out)
		if l := ledgerFor(db); l != nil {
			l.mu.Lock()
			l.lastCheckpoint, l.checkpointKnown = out, true
			l.mu.Unlock()
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, CheckpointDeadline)
	defer cancel()
	ctx = WithClass(ctx, ClassMaintenance)
	ctx = context.WithValue(ctx, checkpointReaderBypassKey{}, true)
	// One dedicated connection, because a PRAGMA through the pool lands on
	// whichever connection it is handed; the same rule the statistics pass learned.
	conn, err := ReadHandle(ctx, db).Conn(ctx)
	if err != nil {
		out.Err = err
		return out
	}
	defer conn.Close()
	var priorBusy int
	if err = conn.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&priorBusy); err != nil {
		out.Err = err
		return out
	}
	// Register restoration before changing the pragma: even a cancellation
	// racing a successful SET must not return altered policy to the pool.
	defer func() {
		if cleanupErr := restoreCheckpointBusyTimeout(conn, priorBusy); cleanupErr != nil {
			out.Err = errors.Join(out.Err, cleanupErr)
		}
	}()
	if _, err = conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", CheckpointBusyTimeout.Milliseconds())); err != nil {
		out.Err = err
		return out
	}
	copyFence, copyIdle := WriteGate().checkpointCopyFence()
	if err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&out.Busy, &out.LogFrames, &out.Checkpoint); err != nil {
		out.Err = err
		return out
	}
	out.BeforeLogFrames = out.LogFrames
	out.BeforeCheckpointedFrames = out.Checkpoint
	if knownWALPath == "" {
		file, pathErr := checkpointDatabaseFile(ctx, conn)
		if pathErr != nil {
			out.Err = pathErr
			return out
		}
		if file != "" {
			knownWALPath = file + "-wal"
		}
	}
	if knownWALPath != "" {
		if info, statErr := os.Stat(knownWALPath); statErr == nil {
			out.BeforeFileBytes = info.Size()
		} else if !errors.Is(statErr, os.ErrNotExist) {
			out.Err = statErr
			return out
		}
	}
	ledgerSoft, ledgerHard := ledgerFor(db).pressure()
	var pageSize int64
	if err = conn.QueryRowContext(ctx, `PRAGMA page_size`).Scan(&pageSize); err != nil {
		out.Err = err
		return out
	}
	frameBytes := pageSize + 24
	hardPressure := int64(out.LogFrames)*frameBytes+32 >= CheckpointHardFileBytes || out.BeforeFileBytes >= CheckpointHardFileBytes || ledgerHard || ledgerFor(db).reservationPressure()
	scope := checkpointReaderScope(conn)
	if scope != nil {
		out.Readers = scope.stats()
	} else {
		out.Readers = ReaderLifetimes()
	}
	if int64(out.LogFrames)*frameBytes+32 < 64<<20 && !hardPressure && !ledgerSoft {
		return out
	}
	if hardPressure {
		return recoverWAL(ctx, conn, scope, ledgerFor(db), out)
	}
	// TRUNCATE blocks until every reader has drained, so below the hard
	// threshold it only runs when nothing is writing or waiting to write and no
	// foreground request is in flight.
	if WriteGate().ActiveOrWaiting() || ForegroundWorkActive() {
		return out
	}
	// TRUNCATE must only reset an already copied log. Its busy timeout bounds
	// lock waiting, not page copying. A partial pass, or a writer in flight at
	// the start of that pass, cannot establish a safe reset point.
	generation, idle := WriteGate().checkpointCopyFence()
	if !copyIdle || !idle || generation != copyFence {
		return out
	}
	if out.Busy != 0 || out.Checkpoint != out.LogFrames {
		return out
	}
	release, err := WriteGate().Acquire(ctx, ClassMaintenance)
	if err != nil {
		out.Err = err
		return out
	}
	defer release()
	// A writer may have committed between PASSIVE and this acquisition. Even
	// one new batch means TRUNCATE could copy a new tail under the gate. Let
	// the next PASSIVE pass copy it instead. Count potential writers, not
	// delayed commit notifications; closed read-only readiness probes do not
	// change this generation. Autocommit, rollback and in-flight writes do.
	if generation, _ = WriteGate().checkpointCopyFence(); generation != copyFence+1 {
		return out
	}
	out.Mode = "truncate"
	if err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&out.Busy, &out.LogFrames, &out.Checkpoint); err != nil {
		out.Err = err
	}
	if out.Err == nil && out.Busy == 0 {
		ledgerFor(db).reset()
	}
	return out
}

// RecoveryHolderWait bounds how long a recovery attempt waits for the writer
// already holding the gate to finish, once at the hold and once at the pause.
const RecoveryHolderWait = 250 * time.Millisecond

// recoverWAL is the hard-pressure reset (r3 §7, ordered by the lead errata):
// hold new writers (a bounded number of security fences still pass), let the
// current holder finish, drain overlapping readers, copy outside the gate,
// pause every acquisition, prove the copy, and take the one reserved turn that
// may reset the log. No gate is held while waiting, draining or copying, so a
// pinned reader or slow storage never holds the writer; and because nothing
// else can begin, a reset no longer queues behind users. Failure ends the
// attempt, reopens everything and backs off; it never copies a tail under the
// gate.
func recoverWAL(ctx context.Context, conn *sql.Conn, scope *readerScope, ledger *walLedger, in CheckpointResult) (out CheckpointResult) {
	out = in
	if scope != nil {
		if backoff := scope.pressureBackoff(); backoff > 0 {
			out.Outcome, out.BackoffMs = "reader-backoff", backoff.Milliseconds()
			return out
		}
	}
	gate := WriteGate()
	epoch, err := gate.enterPressureHold()
	if err != nil {
		// Another owner's attempt is running; this pass defers to it.
		out.Outcome = "recovery-busy"
		return out
	}
	defer gate.endRecovery(epoch)
	// An owner-long read may pin the log for minutes; at hard pressure it
	// ends with a retryable refusal so the reset can proceed.
	cancelLongRead()
	// A writer that outlasts the bounded wait is an ordinary deferral, not a
	// failed attempt: it charges no reader drain and no backoff.
	writerBusy := func() CheckpointResult {
		out.Outcome = "writer-busy"
		return out
	}
	holderCtx, holderCancel := context.WithTimeout(ctx, RecoveryHolderWait)
	err = gate.waitIdle(holderCtx)
	holderCancel()
	if err != nil {
		return writerBusy()
	}
	reset, deferred := false, false
	if scope != nil {
		reopen, drained, backoff := scope.pause()
		if reopen == nil {
			out.Outcome, out.BackoffMs = "reader-backoff", backoff.Milliseconds()
			return out
		}
		// Registered at once: cancellation, error and panic all reopen
		// readers. A reader that will not drain backs off; a reset, or a
		// writer deferral, does not.
		defer func() {
			reopen(out.Err == nil && (reset || deferred))
			out.BackoffMs = scope.pressureBackoff().Milliseconds()
		}()
		drainCtx, drainCancel := context.WithTimeout(ctx, CheckpointReaderDrainDeadline)
		drainStart := time.Now()
		err = waitReaderDrain(drainCtx, drained)
		out.DrainMs = time.Since(drainStart).Milliseconds()
		drainCancel()
		if err != nil {
			out.Outcome = "reader-timeout"
			if ctx.Err() != nil {
				out.Err = ctx.Err()
			}
			return out
		}
		// The readers that pinned the first pass have drained. New readers
		// may run while the released tail is copied: slow storage must not
		// stop requests.
		scope.resumePressureReaders()
	}
	if err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&out.Busy, &out.LogFrames, &out.Checkpoint); err != nil {
		out.Err = err
		return out
	}
	// The final interval: no acquisition of any class begins, so the proof
	// copy below cannot be overtaken. A security fence admitted under the
	// hold finishes first.
	if err = gate.enterBeginPause(epoch); err != nil {
		out.Err = err
		return out
	}
	holderCtx, holderCancel = context.WithTimeout(ctx, RecoveryHolderWait)
	err = gate.waitIdle(holderCtx)
	holderCancel()
	if err != nil {
		deferred = true
		return writerBusy()
	}
	copyFence, copyIdle := gate.checkpointCopyFence()
	if err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&out.Busy, &out.LogFrames, &out.Checkpoint); err != nil {
		out.Err = err
		return out
	}
	if !copyIdle || out.Busy != 0 || out.Checkpoint != out.LogFrames {
		return out
	}
	if scope != nil {
		// Readers admitted during copying may still own WAL read marks. Drain
		// them briefly, now that there is provably no tail left to copy.
		drained := scope.pausePressureReaders()
		if drained == nil {
			out.Err = errors.New("dbwork: checkpoint pressure owner lost")
			return out
		}
		drainCtx, drainCancel := context.WithTimeout(ctx, CheckpointReaderDrainDeadline)
		drainStart := time.Now()
		err = waitReaderDrain(drainCtx, drained)
		out.DrainMs += time.Since(drainStart).Milliseconds()
		drainCancel()
		if err != nil {
			out.Outcome = "reader-timeout"
			if ctx.Err() != nil {
				out.Err = ctx.Err()
			}
			return out
		}
	}
	turnCtx, turnCancel := context.WithTimeout(ctx, RecoveryHolderWait)
	release, err := gate.acquireRecoveryTurn(turnCtx, epoch)
	turnCancel()
	if err != nil {
		deferred = true
		return writerBusy()
	}
	defer release()
	// The reserved turn is the only acquisition since the proof copy, so it
	// moved the generation exactly once. Anything else means a writer slipped
	// in, and the next pass copies its tail outside the gate.
	if generation, _ := gate.checkpointCopyFence(); generation != copyFence+1 {
		deferred = true
		return writerBusy()
	}
	out.Mode = "truncate"
	if err = conn.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&out.Busy, &out.LogFrames, &out.Checkpoint); err != nil {
		out.Err = err
		return out
	}
	reset = out.Busy == 0
	if reset {
		ledger.reset()
	}
	return out
}

// Cleanup must survive the request context being cancelled. A connection whose
// original policy cannot be restored is discarded rather than silently loaned
// to subsequent background work with a different contention budget.
func restoreCheckpointBusyTimeout(conn *sql.Conn, prior int) error {
	ctx, cancel := context.WithTimeout(context.Background(), checkpointCleanupDeadline)
	defer cancel()
	_, err := conn.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", prior))
	if err == nil {
		return nil
	}
	_ = conn.Raw(func(any) error { return driver.ErrBadConn })
	return fmt.Errorf("dbwork: restore checkpoint busy timeout: %w", err)
}
