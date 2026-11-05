package governor

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"sync"
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
func ReadWAL(path string) (WALState, error) {
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
}

// WALGate is owned by ONE governor/writer pair. Every completed writer turn
// invalidates observations, as does a private checkpoint turn. The driver must
// publish a fresh post-return sample
// before any new writer. This prevents repeated admissions against stale space.
// The recipe's WALBytes forecast must include the whole native/receipt/commit
// operation. An underestimated forecast cannot be repaired by a timer.
type WALGate struct {
	mu                sync.Mutex
	policy            WALPolicy
	version           uint64
	writer            bool
	checkpoint        bool
	pendingCheckpoint *checkpointTicket
	nextCheckpoint    time.Time
	valid             bool
	state             WALState
	at                time.Time
	previous          WALState
	resets            uint64
}

func NewWALGate(p WALPolicy) (*WALGate, error) {
	if p.HardBytes <= 0 || p.ReserveBytes <= 0 || p.ReserveBytes >= p.HardBytes ||
		p.SoftDebtBytes <= 0 || p.ActivePressureBytes <= 0 || p.ActivePressureBytes > p.HardBytes-p.ReserveBytes ||
		p.MaxSampleAge <= 0 {
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
	if token != w.version || w.writer || w.checkpoint {
		return ErrWALObservation
	}
	w.valid = false
	if !validWAL(s) || at.IsZero() {
		return ErrWALObservation
	}
	if w.previous.Frames != 0 && (s.Salt != w.previous.Salt || s.Frames < w.previous.Frames) {
		w.resets++
	}
	w.previous = s
	w.state = s
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
	if !w.mu.TryLock() {
		return Capacity
	}
	defer w.mu.Unlock()
	w.expireCheckpointTicket(now)
	if w.writer || w.checkpoint || w.pendingCheckpoint != nil {
		return Capacity
	}
	if !w.valid || now.Before(w.at) || now.Sub(w.at) > w.policy.MaxSampleAge {
		return FailSafe
	}
	s := w.state
	if s.PhysicalBytes > w.policy.HardBytes {
		return FailSafe
	}
	limit := w.policy.HardBytes
	if r.Class == Background || r.Class == ScanMinimum {
		limit -= w.policy.ReserveBytes
		if s.UncheckpointedBytes >= w.policy.SoftDebtBytes ||
			(s.ActiveBytes >= w.policy.ActivePressureBytes && s.Backfill != s.Frames) {
			return Capacity
		}
	}
	header := int64(0)
	if s.ActiveBytes == 0 {
		header = 32
	}
	if r.Need[WALBytes] <= 0 || header > limit-s.ActiveBytes || r.Need[WALBytes] > limit-s.ActiveBytes-header {
		return Capacity
	}
	w.writer = true
	w.valid = false
	w.version++
	return 0
}

func (w *WALGate) returned() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writer = false
	w.valid = false
	w.version++
	w.signalCheckpointTicket()
}

// ResetCount counts actual observed salt/frame reuse, never PASSIVE completions.
func (w *WALGate) ResetCount() uint64 { w.mu.Lock(); defer w.mu.Unlock(); return w.resets }

// A checkpoint may repay pressure even while WAL observations are stale. Its
// driver still needs a whole-prefix forecast and a fresh same-file observation
// before native entry. This is an exclusion primitive, not a service policy.
func (w *WALGate) reserveCheckpoint(ticket *checkpointTicket, now time.Time) RefusalCode {
	if !w.mu.TryLock() {
		return Capacity
	}
	defer w.mu.Unlock()
	w.expireCheckpointTicket(now)
	if w.writer || w.checkpoint {
		return Capacity
	}
	if w.pendingCheckpoint != ticket {
		return Capacity
	}
	if ticket != nil && (ticket.gate != w || !now.Before(ticket.expires)) {
		return Deadline
	}
	w.pendingCheckpoint = nil
	w.checkpoint = true
	w.valid = false
	w.version++
	return 0
}

// Only exact private grant retirement reaches this boundary, after native
// statements/connection return. Cancel/expiry/fence never release the turn.
// A fresh Observation-before-read/Publish is required before another writer.
func (w *WALGate) checkpointReturned() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.checkpoint = false
	w.valid = false
	w.version++
}
