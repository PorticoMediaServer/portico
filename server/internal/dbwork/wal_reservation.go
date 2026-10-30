package dbwork

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/diskspace"
)

const (
	walSoftBytes      = int64(128 << 20)
	walHardBytes      = int64(192 << 20)
	walRecoveryBytes  = int64(32 << 20)
	walEnvelopeBytes  = int64(256 << 20)
	walEmergencyBytes = int64(32 << 20)
	walCapacityWait   = 5 * time.Second
)

// WALReservationStats distinguishes source qualification from measurement.
// A ledger with unqualified writers is NOT a database-wide WAL bound.
type WALReservationStats struct {
	DebitedBytes      int64
	AdmissionBytes    int64
	PhysicalBytes     int64
	QualifiedWrites   uint64
	UnqualifiedWrites uint64
	MeasuredBytes     uint64
	Overruns          uint64
	UnknownIO         uint64
	Resets            uint64
}

// walLedger is shared by live handles for one native filename, never tied to
// an SQL connection. Recycling a writer cannot forget failure/spill debits.
type walLedger struct {
	mu                       sync.Mutex
	path                     string
	pageSize                 int64
	stats                    WALReservationStats
	free, spent, probedSpent int64
	probeOK                  bool
	native                   bool
	// A source-plan overrun disables enforcement for that plan; no successful
	// COMMIT is turned into a refusal. Its next write remains instrumented.
	blocked            map[string]bool
	lastCheckpoint     CheckpointResult
	checkpointKnown    bool
	reservationBlocked bool
}

type walRequest struct {
	ledger        *walLedger
	name          string
	bytes, growth int64
	qualified     bool
	class         Class
}

type walRequestKey struct{}
type reservedWALKey struct{}

// Only this operation currently has a source proof: advanceHold updates the
// state of one singleton row, no indexed column, FK or trigger program. Four
// frames include the leaf and conservative pager/overflow headroom. The
// publication/deletion/other hold operations are deliberately NOT covered.
const BackupHoldPhasePlanName = "backup-hold-phase"
const backupHoldPhaseSQL = `UPDATE backup_blob_holds SET state=? WHERE singleton=1 AND backup_id=? AND process_epoch=?`

func newWALLedger(path string, pageSize int64) *walLedger {
	l := &walLedger{path: path, pageSize: pageSize, blocked: make(map[string]bool)}
	l.probe()
	l.stats.DebitedBytes = l.stats.PhysicalBytes
	l.stats.AdmissionBytes = l.stats.PhysicalBytes
	return l
}

func ledgerFor(db *sql.DB) *walLedger {
	if db != nil {
		if d, ok := db.Driver().(*splitDriver); ok {
			return d.wal
		}
	}
	return nil
}

func walContext(ctx context.Context, db *sql.DB, class Class, query string) context.Context {
	return walContextForLedger(ctx, ledgerFor(db), class, query)
}

func walContextForLedger(ctx context.Context, l *walLedger, class Class, query string) context.Context {
	if l == nil {
		return ctx
	}
	p := planFor(ctx, class)
	pages := p.Pages
	if pages <= 0 {
		pages = DefaultPlan(class).Pages
	}
	qualified := l.native && p.Name == BackupHoldPhasePlanName && query == backupHoldPhaseSQL
	if qualified {
		pages = 4
	}
	// SQLite frames have a 24-byte header; a reset log also has its 32-byte
	// header. Rounding never assumes the default 4 KiB page size.
	r := &walRequest{ledger: l, name: p.Name, bytes: 32 + pages*(l.pageSize+24), growth: pages * l.pageSize, qualified: qualified, class: class}
	return context.WithValue(ctx, walRequestKey{}, r)
}

func (r *walRequest) ready() bool {
	if r == nil {
		return true
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.blocked[r.name] && r.class.Background() {
		return false
	}
	if !r.qualified || l.blocked[r.name] {
		return true
	}
	if !l.probeOK || l.free-(l.spent-l.probedSpent) < r.bytes+r.growth+walEmergencyBytes+max(0, walEnvelopeBytes-l.stats.AdmissionBytes) {
		return false
	}
	limit := walHardBytes
	if r.class == ClassSecurityFence {
		limit += walRecoveryBytes
	}
	if r.class.Background() {
		limit = walSoftBytes
	}
	if l.stats.AdmissionBytes+r.bytes > limit {
		l.reservationBlocked = true
		return false
	}
	return true
}

// take runs once with the gate selected, before a connection/BEGIN is taken.
// Failure to obtain the connection or finish SQL never refunds this debit.
func (r *walRequest) take() {
	if r == nil {
		return
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stats.DebitedBytes += r.bytes
	l.spent += r.bytes + r.growth
	if r.qualified && !l.blocked[r.name] {
		l.stats.QualifiedWrites++
		l.stats.AdmissionBytes += r.bytes
	} else {
		l.stats.UnqualifiedWrites++
	}
}

func (r *walRequest) measured(attempted uint64, known bool) {
	if r == nil {
		return
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	if !known {
		l.stats.UnknownIO++
		return
	}
	l.stats.MeasuredBytes += attempted
	if attempted > uint64(r.bytes) {
		extra := int64(attempted) - r.bytes
		l.stats.DebitedBytes += extra
		l.spent += extra
		l.stats.Overruns++
		if r.qualified {
			l.stats.AdmissionBytes += extra
			l.blocked[r.name] = true
		}
	}
}

// probe is called outside the gate by the existing checkpoint owner. Cached
// free space is reduced by every intervening reservation, including failures.
func (l *walLedger) probe() {
	if l == nil {
		return
	}
	l.mu.Lock()
	spentAtProbe := l.spent
	l.mu.Unlock()
	physical, statErr := checkpointFileBytes(l.path + "-wal")
	free, err := diskspace.Free(filepath.Dir(l.path))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.probeOK = statErr && err == nil
	if statErr {
		l.stats.PhysicalBytes = physical
		l.stats.DebitedBytes = max(l.stats.DebitedBytes, physical)
		l.stats.AdmissionBytes = max(l.stats.AdmissionBytes, physical)
	}
	if err == nil {
		l.free = free
		l.probedSpent = spentAtProbe
	}
}

func (l *walLedger) pressure() (soft, hard bool) {
	if l == nil {
		return false, false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats.AdmissionBytes >= walSoftBytes, l.stats.AdmissionBytes >= walHardBytes
}

// reservationPressure is independent of physical WAL length. Qualified media
// can exhaust its 128MiB admission ledger while the file remains below the hard
// physical threshold; a soft checkpoint cannot reset behind its waiting ticket.
func (l *walLedger) reservationPressure() bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.reservationBlocked
}

// reset is called only while the checkpoint owner's verified reset turn still
// holds the gate. A PASSIVE copy or an observed file shrink cannot reclaim it.
func (l *walLedger) reset() {
	if l == nil {
		return
	}
	info, err := os.Stat(l.path + "-wal")
	if err != nil && !os.IsNotExist(err) {
		return
	}
	var size int64
	if info != nil {
		size = info.Size()
	}
	if size != 0 {
		return
	}
	l.mu.Lock()
	l.stats.PhysicalBytes = size
	l.stats.DebitedBytes = size
	l.stats.AdmissionBytes = size
	l.reservationBlocked = false
	l.stats.Resets++
	l.mu.Unlock()
}

func (l *walLedger) snapshot() WALReservationStats {
	if l == nil {
		return WALReservationStats{}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stats
}

// WALReservations is internal diagnostic data; no path or actor is exposed.
func WALReservations(db *sql.DB) WALReservationStats { return ledgerFor(db).snapshot() }

func wakeWALWaiters() {
	gate.mu.Lock()
	if !gate.active {
		gate.grant = gate.pickWinnerLocked()
	}
	gate.signalLocked()
	gate.mu.Unlock()
}

// DSN aliases of the same native filename must share reservations. One reference
// belongs to each live reader scope, which survives borrowed native connections.
var liveWALLedgers = struct {
	sync.Mutex
	byPath map[string]*walLedger
	refs   map[*walLedger]int
}{byPath: make(map[string]*walLedger), refs: make(map[*walLedger]int)}

func shareWALLedger(scope *readerScope, candidate *walLedger) *walLedger {
	if scope == nil {
		return candidate
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.wal != nil {
		return scope.wal
	}
	liveWALLedgers.Lock()
	defer liveWALLedgers.Unlock()
	l := liveWALLedgers.byPath[candidate.path]
	if l == nil {
		l = candidate
		liveWALLedgers.byPath[l.path] = l
	}
	liveWALLedgers.refs[l]++
	scope.wal = l
	return l
}

func releaseWALLedger(scope *readerScope) {
	scope.mu.Lock()
	l := scope.wal
	scope.mu.Unlock()
	if l == nil {
		return
	}
	liveWALLedgers.Lock()
	defer liveWALLedgers.Unlock()
	liveWALLedgers.refs[l]--
	if liveWALLedgers.refs[l] == 0 {
		delete(liveWALLedgers.refs, l)
		delete(liveWALLedgers.byPath, l.path)
	}
}

// Unqualified plan estimates are shadow telemetry: enabling their speculative
// reservations would also enable new reset pauses for unqualified handlers.
// Actual observed retained write extent still feeds the physical controller.
func (r *walRequest) retained(end int64) {
	if r == nil || end < 0 {
		return
	}
	l := r.ledger
	l.mu.Lock()
	defer l.mu.Unlock()
	l.stats.AdmissionBytes = max(l.stats.AdmissionBytes, end)
}
