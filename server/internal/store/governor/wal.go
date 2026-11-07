package governor

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"time"
)

var ErrWALObservation = errors.New("invalid or stale WAL observation")

// WALState keeps physical file length, active committed prefix and uncopied debt
// distinct. None is an exact oracle for the NORMAL prefix a FULL commit will sync.
type WALState struct {
	PhysicalBytes, ActiveBytes, UncheckpointedBytes int64
	Frames, Backfill                                uint32
	PageSize                                        uint32
	Salt                                            [8]byte
}

// ReadWAL ports Q's read-only WAL-index observation. A racing/torn/unsupported
// header fails closed; observation is advisory evidence, never SQLite authority.
// Call outside Try, with a token from WALGate.Observation, then Publish it.
func readWALOnce(path string) (WALState, error) {
	var w WALState
	wal, err := os.Open(path + "-wal")
	if err != nil {
		return w, err
	}
	defer wal.Close()
	info, err := wal.Stat()
	if err != nil {
		return w, err
	}
	w.PhysicalBytes = info.Size()
	shm, err := os.Open(path + "-shm")
	if err != nil {
		return w, err
	}
	defer shm.Close()
	var first, second [100]byte
	if _, err = shm.ReadAt(first[:], 0); err != nil {
		return w, err
	}
	if _, err = shm.ReadAt(second[:], 0); err != nil {
		return w, err
	}
	if first != second || !bytes.Equal(first[:48], first[48:96]) ||
		binary.NativeEndian.Uint32(first[:4]) != 3007000 || first[12] != 1 {
		return w, ErrWALObservation
	}
	w.Frames = binary.NativeEndian.Uint32(first[16:20])
	w.Backfill = binary.NativeEndian.Uint32(first[96:100])
	w.PageSize = uint32(binary.NativeEndian.Uint16(first[14:16]))
	if w.PageSize == 1 {
		w.PageSize = 65536
	}
	// SQLite recreates an empty WAL after a clean open/TRUNCATE. Its index
	// legitimately has no page size yet. Validate the database/header rather
	// than requiring an ungoverned first write to bootstrap admission.
	if w.Frames == 0 && w.Backfill == 0 && (w.PhysicalBytes == 0 || w.PhysicalBytes == 32) {
		var header [32]byte
		page, err := emptyWALPageSize(path, wal, w.PhysicalBytes, &header)
		if err != nil || (w.PageSize != 0 && w.PageSize != page) {
			return w, ErrWALObservation
		}
		w.PageSize = page
		copy(w.Salt[:], first[32:40])
		if w.PhysicalBytes == 32 {
			copy(w.Salt[:], header[16:24])
		}
		return w, nil // physical allocation is distinct; active/debt are zero
	}
	if w.Backfill > w.Frames || w.PageSize < 512 || w.PageSize > 65536 || w.PageSize&(w.PageSize-1) != 0 {
		return w, ErrWALObservation
	}
	copy(w.Salt[:], first[32:40])
	w.ActiveBytes = 32 + int64(w.Frames)*(int64(w.PageSize)+24)
	w.UncheckpointedBytes = int64(w.Frames-w.Backfill) * (int64(w.PageSize) + 24)
	if w.ActiveBytes > w.PhysicalBytes {
		return w, ErrWALObservation
	}
	return w, nil
}

// Only the empty-WAL bootstrap reads the fixed database header. Missing files,
// non-WAL databases and short/torn/invalid headers still fail closed.
func emptyWALPageSize(path string, wal *os.File, size int64, header *[32]byte) (uint32, error) {
	db, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var a, b [100]byte
	if _, err = db.ReadAt(a[:], 0); err != nil {
		return 0, err
	}
	if _, err = db.ReadAt(b[:], 0); err != nil {
		return 0, err
	}
	page := uint32(binary.BigEndian.Uint16(a[16:18]))
	if page == 1 {
		page = 65536
	}
	if a != b || string(a[:16]) != "SQLite format 3\x00" || a[18] != 2 || a[19] != 2 ||
		page < 512 || page > 65536 || page&(page-1) != 0 {
		return 0, ErrWALObservation
	}
	if size == 32 {
		var x, y [32]byte
		if _, err = wal.ReadAt(x[:], 0); err != nil {
			return 0, err
		}
		if _, err = wal.ReadAt(y[:], 0); err != nil {
			return 0, err
		}
		magic := binary.BigEndian.Uint32(x[:4])
		if x != y || (magic != 0x377f0682 && magic != 0x377f0683) ||
			binary.BigEndian.Uint32(x[4:8]) != 3007000 || binary.BigEndian.Uint32(x[8:12]) != page {
			return 0, ErrWALObservation
		}
		var order binary.ByteOrder = binary.LittleEndian
		if magic&1 != 0 {
			order = binary.BigEndian
		}
		var s0, s1 uint32
		for i := 0; i < 24; i += 8 {
			s0 += order.Uint32(x[i:i+4]) + s1
			s1 += order.Uint32(x[i+4:i+8]) + s0
		}
		if s0 != binary.BigEndian.Uint32(x[24:28]) || s1 != binary.BigEndian.Uint32(x[28:32]) {
			return 0, ErrWALObservation
		}
		*header = x
	}
	info, err := wal.Stat()
	if err != nil || info.Size() != size {
		return 0, ErrWALObservation
	}
	return page, nil
}

type WALPolicy struct {
	HardBytes, ReserveBytes, SoftDebtBytes, ActivePressureBytes int64
	MaxSampleAge                                                time.Duration
	ConcurrentPassive                                           bool          // trusted opt-in; one CP may overlap W and foreground DB reads
	CheckpointMaxHold                                           time.Duration // separate maintenance window, never a W window
}

// WALGate belongs to ONE governor/writer composition. Every physical writer
// return invalidates its generation and requires a fresh post-return sample.
// Serialized CP does too. Opt-in concurrent PASSIVE has separate custody and
// cannot append/reset, so a stable writer-free sample may be published during
// CP. Backfill never grants speculative append headroom. Complete W forecasts
// include native/receipt/commit and restart-header work before BEGIN.
type WALGate struct {
	mu                       ledgerMutex
	policy                   WALPolicy
	version                  uint64
	writer                   bool
	blockedWAL, blockedLimit int64 // last real space refusal; no speculative reset credit
	checkpoint               bool
	keeper                   *CheckpointKeeper // sticky composition: no unprotected fallback
	pendingCheckpoint        *checkpointTicket
	nextCheckpoint           time.Time
	valid                    bool
	state                    WALState
	at                       time.Time
	previous                 WALState
	havePrevious             bool
	resets                   uint64
	writerClass              Class
	writerForecast           WALRestartObservation
	awaitWriterSample        bool
	lastRestart              WALRestartObservation
	restartSequence          uint64
}

func NewWALGate(p WALPolicy) (*WALGate, error) {
	if p.HardBytes <= 0 || p.ReserveBytes <= 0 || p.ReserveBytes >= p.HardBytes ||
		p.SoftDebtBytes <= 0 || p.ActivePressureBytes <= 0 || p.ActivePressureBytes > p.HardBytes-p.ReserveBytes ||
		p.MaxSampleAge <= 0 || (p.ConcurrentPassive && (p.CheckpointMaxHold <= 0 || p.CheckpointMaxHold > time.Minute)) || (!p.ConcurrentPassive && p.CheckpointMaxHold != 0) {
		return nil, ErrWALObservation
	}
	return &WALGate{policy: p}, nil
}

// Observation captures the generation BEFORE reading files, not after. Samples
// collected before/during a writer cannot be published after its retirement.
func (w *WALGate) Observation() uint64 { w.mu.Lock(); defer w.mu.Unlock(); return w.version }

func (w *WALGate) Publish(token uint64, s WALState, at time.Time) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if token != w.version || w.writer || (w.checkpoint && !w.policy.ConcurrentPassive) {
		return ErrWALObservation
	}
	w.valid = false
	if !validWAL(s) || at.IsZero() {
		return ErrWALObservation
	}
	if w.previous.Frames != 0 && (s.Salt != w.previous.Salt || s.Frames < w.previous.Frames) {
		w.resets++
	}
	initial := w.havePrevious && w.previous.Frames == 0 && s.Frames > 0
	reset := w.previous.Frames != 0 && (s.Salt != w.previous.Salt || s.Frames < w.previous.Frames)
	if initial || reset {
		w.restartSequence++
		receipt := WALRestartObservation{Sequence: w.restartSequence, ObservedAt: at, InitialHeader: initial, Before: w.previous, After: s}
		if w.awaitWriterSample && s.Frames > 0 {
			receipt.WriterKnown = true
			receipt.Class = w.writerClass
			receipt.ForecastHold = w.writerForecast.ForecastHold
			receipt.ForecastIOBytes = w.writerForecast.ForecastIOBytes
			receipt.ForecastWALBytes = w.writerForecast.ForecastWALBytes
			receipt.Risk = w.writerForecast.Risk
		}
		w.lastRestart = receipt
	}
	w.awaitWriterSample = false
	w.havePrevious = true
	w.previous = s
	w.state = s
	if w.blockedWAL > 0 && s.ActiveBytes+32 <= w.blockedLimit && w.blockedWAL <= w.blockedLimit-s.ActiveBytes-32 {
		w.blockedWAL, w.blockedLimit = 0, 0
	}
	w.at = at
	w.valid = true
	return nil
}

func validWAL(s WALState) bool {
	if s.Frames == 0 && s.ActiveBytes == 0 {
		return s.PageSize >= 512 && s.PageSize <= 65536 && s.PageSize&(s.PageSize-1) == 0 &&
			s.Backfill == 0 && s.UncheckpointedBytes == 0 && (s.PhysicalBytes == 0 || s.PhysicalBytes == 32)
	}
	return s.PageSize >= 512 && s.PageSize <= 65536 && s.PageSize&(s.PageSize-1) == 0 && s.Backfill <= s.Frames &&
		s.ActiveBytes == 32+int64(s.Frames)*(int64(s.PageSize)+24) && s.PhysicalBytes >= s.ActiveBytes &&
		s.UncheckpointedBytes == int64(s.Frames-s.Backfill)*(int64(s.PageSize)+24)
}

func (w *WALGate) reserve(r Request, now time.Time) RefusalCode {
	code, _, _ := w.reserveForLedger(context.Background(), r, now, nil, r.Deadline)
	return code
}

func (w *WALGate) reserveForLedger(ctx context.Context, r Request, now time.Time, owner *ledger, until time.Time) (RefusalCode, RefusalCause, time.Time) {
	if err := w.mu.lockUntil(ctx, until, now); err != nil {
		return Deadline, admissionWaitCause(err), time.Time{}
	}
	defer w.mu.Unlock()
	if owner != nil {
		now = owner.config.Now()
	}
	if !until.After(now) {
		return Deadline, CauseDeadline, time.Time{}
	}
	if err := ctx.Err(); err != nil {
		return Deadline, admissionWaitCause(err), time.Time{}
	}
	w.expireCheckpointTicket(now)
	if (w.policy.ConcurrentPassive && w.keeper == nil) || (w.keeper != nil && !w.keeper.admits(owner, now)) {
		return FailSafe, CauseKeeper, time.Time{}
	}
	if w.writer {
		return Capacity, CauseWriterActive, time.Time{}
	}
	if !w.policy.ConcurrentPassive && (w.checkpoint || w.pendingCheckpoint != nil) {
		return Capacity, CauseCheckpointActive, time.Time{}
	}
	if !w.fresh(now) {
		return FailSafe, CauseWALSample, time.Time{}
	}
	if (w.policy.ConcurrentPassive && r.RestartRisk == (WALRestartRisk{})) || (r.RestartRisk != (WALRestartRisk{}) && r.RestartRisk != w.restartRisk()) {
		return FailSafe, CauseWALRisk, time.Time{}
	}
	s := w.state
	if s.PhysicalBytes > w.policy.HardBytes {
		return FailSafe, CauseWALPhysical, time.Time{}
	}
	limit := w.policy.HardBytes
	if r.Class == Background || r.Class == ScanMinimum {
		limit -= w.policy.ReserveBytes
		if s.UncheckpointedBytes >= w.policy.SoftDebtBytes {
			return Capacity, CauseWALDebt, time.Time{}
		}
		if s.ActiveBytes >= w.policy.ActivePressureBytes && s.Backfill != s.Frames {
			return Capacity, CauseWALActive, time.Time{}
		}
	}
	header := int64(0)
	if s.ActiveBytes == 0 {
		header = 32
	}
	if r.Need[WALBytes] <= 0 || header > limit-s.ActiveBytes || r.Need[WALBytes] > limit-s.ActiveBytes-header {
		if r.Need[WALBytes] > 0 && r.Need[WALBytes] <= limit-32 {
			w.blockedWAL = r.Need[WALBytes]
			w.blockedLimit = limit
		}
		return Capacity, CauseWALSpace, time.Time{}
	}
	w.blockedWAL, w.blockedLimit = 0, 0
	w.writerClass = r.Class
	w.writerForecast = WALRestartObservation{ForecastHold: r.MaxHold, ForecastIOBytes: r.Need[IOBytes], ForecastWALBytes: r.Need[WALBytes], Risk: r.RestartRisk}
	w.awaitWriterSample = false
	w.writer = true
	w.valid = false
	w.version++
	return 0, "", now
}

func (w *WALGate) returned() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writer = false
	w.awaitWriterSample = true
	w.valid = false
	w.version++
	w.signalCheckpointTicket()
}

// ResetCount counts actual observed salt/frame reuse, never PASSIVE completions.
func (w *WALGate) ResetCount() uint64 { w.mu.Lock(); defer w.mu.Unlock(); return w.resets }

// A checkpoint may repay pressure even while WAL observations are stale. Its
// driver still needs a whole-prefix forecast and same-file observation. In
// concurrent mode only CP/CP is excluded; recovery separately drains W/readers.
func (w *WALGate) reserveCheckpoint(ticket *checkpointTicket, now, deadline time.Time) (RefusalCode, RefusalCause) {
	var owner *ledger
	if ticket != nil {
		owner = ticket.owner
	}
	code, cause, _ := w.reserveCheckpointContext(context.Background(), ticket, now, deadline, owner)
	return code, cause
}
func (w *WALGate) reserveCheckpointContext(ctx context.Context, ticket *checkpointTicket, now, deadline time.Time, owner *ledger) (RefusalCode, RefusalCause, time.Time) {
	if err := w.mu.lockUntil(ctx, deadline, now); err != nil {
		return Deadline, admissionWaitCause(err), time.Time{}
	}
	defer w.mu.Unlock()
	if owner != nil {
		now = owner.config.Now()
	}
	if !deadline.After(now) {
		return Deadline, CauseDeadline, time.Time{}
	}
	if err := ctx.Err(); err != nil {
		return Deadline, admissionWaitCause(err), time.Time{}
	}
	w.expireCheckpointTicket(now)
	if w.checkpoint || (w.writer && (!w.policy.ConcurrentPassive || (ticket != nil && ticket.recovery))) {
		return Capacity, CauseCheckpointActive, time.Time{}
	}
	if ticket != nil && (ticket.gate != w || !now.Before(ticket.expires)) {
		return Deadline, CauseDeadline, time.Time{}
	}
	if w.pendingCheckpoint != ticket {
		return Capacity, CauseCheckpointTicket, time.Time{}
	}
	w.pendingCheckpoint = nil
	w.checkpoint = true
	if !w.policy.ConcurrentPassive || (ticket != nil && ticket.recovery) {
		w.awaitWriterSample = false
		w.valid = false
		w.version++
	}
	return 0, "", now
}

// Only exact private grant retirement reaches this boundary, after native
// statements/connection return. Cancel/expiry/fence never release the turn.
// Serialized mode also requires a fresh Observation-before-read/Publish; the
// concurrent mode preserves the independent writer generation.
func (w *WALGate) checkpointReturned() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.checkpoint = false
	if !w.policy.ConcurrentPassive {
		w.valid = false
		w.version++
	}
}

// A real command can stop fitting before hard-minus-reserve. Its actual space
// refusal also authorizes exclusive recovery; otherwise that legal recipe can
// wedge below the byte threshold. No threshold or legal recipe is reduced.
func (w *WALGate) recoveryPressure(s WALState) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return s.PhysicalBytes <= w.policy.HardBytes && (s.ActiveBytes >= w.policy.HardBytes-w.policy.ReserveBytes ||
		(w.blockedWAL > 0 && (s.ActiveBytes > w.blockedLimit-32 || w.blockedWAL > w.blockedLimit-s.ActiveBytes-32)))
}

// ReadWAL retries only advisory unstable headers, never a writer/transaction.
// Every attempt reopens/revalidates the files. The caller's Observation token
// must still precede the entire read and Publish must validate that generation.
func ReadWAL(path string) (WALState, error) { return ReadWALContext(context.Background(), path) }
func ReadWALContext(ctx context.Context, path string) (WALState, error) {
	return sampleWAL(ctx, func() (WALState, error) { return readWALOnce(path) })
}

const walSampleAttempts = 4
const walSampleBudget = 2 * time.Millisecond

func sampleWAL(ctx context.Context, read func() (WALState, error), clocks ...func() time.Time) (WALState, error) {
	if ctx == nil {
		return WALState{}, ErrWALObservation
	}
	now := time.Now
	if len(clocks) > 0 {
		now = clocks[0]
	}
	until := now().Add(walSampleBudget)
	var state WALState
	var err error
	for n := 0; n < walSampleAttempts; n++ {
		if e := ctx.Err(); e != nil {
			return WALState{}, e
		}
		state, err = read()
		if err == nil || !errors.Is(err, ErrWALObservation) {
			return state, err
		}
		if !now().Before(until) {
			break
		}
	}
	return WALState{}, err
}

// A known writer-generation race cannot invalidate another writer's anchor.
func (w *WALGate) observationCurrent(token uint64) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return token == w.version && !w.writer
}

func (w *WALGate) checkpointPageSize(token uint64) (uint32, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	page := w.state.PageSize
	return page, (token != w.version || w.writer) && page >= 512 && page <= 65536 && page&(page-1) == 0
}
