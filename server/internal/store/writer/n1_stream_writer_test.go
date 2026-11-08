package writer

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Asynchronous raw-stream writer for the long measured targets. The measured
// serial chain appends records between owner turns; the original synchronous
// append took the stream mutex, then marshalled, wrote and fsynced EVERY record
// while holding it. Under device contention that put 0.1-2.6 s stalls between
// owner turns in the harness (never in the product). Here:
//   - at_ns is stamped at the event, BEFORE any lock;
//   - a tiny mutex covers only sequence assignment, marshal and a NON-BLOCKING
//     enqueue (no I/O, never waits on the observer);
//   - one writer goroutine per stream does file.Write in sequence order and fsyncs
//     at most every n1StreamSyncEvery and at close, never per record;
//   - a full queue never blocks the caller and never loses a record silently: the
//     overflowing record is counted (its sequence gap stays visible), append
//     returns an explicit error that stops the run, and close() reports it too.
//
// The writer side measures lock wait, queue wait and write time, so a slow
// observer, disk or socket is visible in the evidence.
const (
	n1StreamQueueRecords = 1 << 14
	n1StreamQueueBytes   = 64 << 20
	n1StreamSyncEvery    = time.Second
	n1StreamSlowKeep     = 16
	n1StreamSampleCap    = 1 << 17
)

type n1StreamItem struct {
	raw       []byte
	seq       uint64
	kind      string
	stampNS   int64 // event time (before the lock)
	lockWait  int64 // stamp -> tiny mutex acquired
	enqueueNS int64 // time of the non-blocking enqueue
}

type n1StreamSlow struct {
	Sequence    uint64 `json:"sequence"`
	Kind        string `json:"kind"`
	StampNS     int64  `json:"at_ns"`
	LockWaitNS  int64  `json:"before_lock_to_after_lock_ns"`
	QueueWaitNS int64  `json:"enqueue_to_write_start_ns"`
	WriteNS     int64  `json:"write_ns"`
}

type n1StreamWriter struct {
	queue chan n1StreamItem
	done  chan struct{}

	mu          sync.Mutex // statistics and writer error only
	queuedBytes int64
	queued      uint64
	written     uint64
	dropped     uint64
	firstDrop   uint64
	lastDrop    uint64
	maxLockWait int64
	maxQueue    int64
	maxWrite    int64
	maxSync     int64
	syncs       uint64
	writes      []int64
	slow        []n1StreamSlow
	err         error
	closed      bool
}

func (s *n1Stream) startAsync(queueRecords int, sink func([]byte) error, sync func() error) {
	w := &n1StreamWriter{queue: make(chan n1StreamItem, queueRecords), done: make(chan struct{})}
	s.async = w
	go w.run(s, sink, sync)
}

// enableAsync turns the stream asynchronous with the real file sink.
func (s *n1Stream) enableAsync() {
	s.startAsync(n1StreamQueueRecords,
		func(b []byte) error { _, err := s.file.Write(b); return err },
		func() error {
			if s.streamOnly {
				return nil // never fsync a socket
			}
			return s.file.Sync()
		})
}

func (s *n1Stream) since() int64 {
	if s.clock != nil {
		return s.clock()
	}
	return time.Since(s.origin).Nanoseconds()
}

func (w *n1StreamWriter) run(s *n1Stream, sink func([]byte) error, sync func() error) {
	defer close(w.done)
	lastSync := time.Now()
	for item := range w.queue {
		start := time.Now()
		queueWait := s.since() - item.enqueueNS
		err := sink(item.raw)
		write := time.Since(start).Nanoseconds()
		w.mu.Lock()
		w.queuedBytes -= int64(len(item.raw))
		w.written++
		w.maxLockWait = max(w.maxLockWait, item.lockWait)
		w.maxQueue = max(w.maxQueue, queueWait)
		w.maxWrite = max(w.maxWrite, write)
		if len(w.writes) < n1StreamSampleCap {
			w.writes = append(w.writes, write)
		}
		w.noteSlow(n1StreamSlow{Sequence: item.seq, Kind: item.kind, StampNS: item.stampNS, LockWaitNS: item.lockWait, QueueWaitNS: queueWait, WriteNS: write})
		if err != nil && w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		if time.Since(lastSync) >= n1StreamSyncEvery {
			w.timedSync(sync)
			lastSync = time.Now()
		}
	}
	w.timedSync(sync)
}

func (w *n1StreamWriter) timedSync(sync func() error) {
	start := time.Now()
	err := sync()
	d := time.Since(start).Nanoseconds()
	w.mu.Lock()
	w.syncs++
	w.maxSync = max(w.maxSync, d)
	if err != nil && w.err == nil {
		w.err = err
	}
	w.mu.Unlock()
}

// noteSlow keeps the worst few records by combined delay. Caller holds w.mu.
func (w *n1StreamWriter) noteSlow(x n1StreamSlow) {
	score := func(v n1StreamSlow) int64 { return v.LockWaitNS + v.QueueWaitNS + v.WriteNS }
	if len(w.slow) < n1StreamSlowKeep {
		w.slow = append(w.slow, x)
		return
	}
	worst := 0
	for i := range w.slow {
		if score(w.slow[i]) < score(w.slow[worst]) {
			worst = i
		}
	}
	if score(x) > score(w.slow[worst]) {
		w.slow[worst] = x
	}
}

func (w *n1StreamWriter) error() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

// appendAsync is called with the event already stamped.
func (s *n1Stream) appendAsync(kind string, fields map[string]any, stamp int64) error {
	w := s.async
	s.mu.Lock()
	lockWait := s.since() - stamp
	defer s.mu.Unlock()
	if s.err != nil {
		return s.err
	}
	if err := w.error(); err != nil {
		s.err = err
		return err
	}
	if w.isClosed() {
		return errors.New("N1 raw stream already closed")
	}
	s.sequence++
	record := make(map[string]any, len(s.base)+len(fields)+3)
	for k, v := range s.base {
		record[k] = v
	}
	for k, v := range fields {
		record[k] = v
	}
	record["kind"], record["sequence"], record["at_ns"] = kind, s.sequence, stamp
	raw, err := marshalRecord(record)
	if err != nil {
		s.err = err
		return err
	}
	item := n1StreamItem{raw: raw, seq: s.sequence, kind: kind, stampNS: stamp, lockWait: lockWait, enqueueNS: s.since()}
	w.mu.Lock()
	over := w.queuedBytes+int64(len(raw)) > n1StreamQueueBytes
	if !over {
		select {
		case w.queue <- item:
			w.queuedBytes += int64(len(raw))
			w.queued++
		default:
			over = true
		}
	}
	if over {
		w.dropped++
		if w.firstDrop == 0 {
			w.firstDrop = s.sequence
		}
		w.lastDrop = s.sequence
	}
	var dropErr error
	if over {
		dropErr = fmt.Errorf("N1 raw stream queue overflow: record %d (%s) dropped; the run fails explicitly, evidence incomplete", s.sequence, kind)
		s.err = dropErr // sticky: the measured chain stops instead of silently continuing
	}
	w.mu.Unlock()
	return dropErr // never blocks; a drop fails the run explicitly (close() reports the total too)
}

func marshalRecord(record map[string]any) ([]byte, error) {
	raw, err := json.Marshal(record)
	if err != nil {
		return nil, err
	}
	if len(raw) > 2<<20 {
		return nil, errors.New("N1 component record exceeds bounded 2 MiB wire")
	}
	return append(raw, '\n'), nil
}

func (w *n1StreamWriter) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// writerStats is attached to the final record so a slow sink is visible.
func (s *n1Stream) writerStats() any {
	w := s.async
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	writes := append([]int64(nil), w.writes...)
	sort.Slice(writes, func(i, j int) bool { return writes[i] < writes[j] })
	pct := func(p float64) any {
		if len(writes) == 0 {
			return nil
		}
		return writes[min(len(writes)-1, int(float64(len(writes))*p))]
	}
	slow := append([]n1StreamSlow(nil), w.slow...)
	sort.Slice(slow, func(i, j int) bool {
		return slow[i].LockWaitNS+slow[i].QueueWaitNS+slow[i].WriteNS > slow[j].LockWaitNS+slow[j].QueueWaitNS+slow[j].WriteNS
	})
	return map[string]any{
		"scope":                    "asynchronous raw stream writer: at_ns stamped before the lock; tiny lock covers sequence/marshal/non-blocking enqueue only; fsync at most every 1 s and at close",
		"transport":                s.transportName(),
		"real_fsync":               !s.streamOnly,
		"sync_calls_scope":         "fsyncs/max_fsync_ns count the writer's sync CALLS; they are real fsyncs only when real_fsync is true (a plain file). On the unix socket transport the sync closure is a no-op.",
		"queued_records":           w.queued,
		"written_records_so_far":   w.written,
		"dropped_records":          w.dropped,
		"first_dropped_sequence":   w.firstDrop,
		"last_dropped_sequence":    w.lastDrop,
		"max_before_to_after_lock": w.maxLockWait,
		"max_enqueue_to_write_ns":  w.maxQueue,
		"max_write_ns":             w.maxWrite,
		"write_p50_ns":             pct(0.5),
		"write_p99_ns":             pct(0.99),
		"fsyncs":                   w.syncs,
		"max_fsync_ns":             w.maxSync,
		"slowest_records":          slow,
	}
}

// close drains the queue, fsyncs once, closes the file and reports any writer
// error or dropped records. For a synchronous stream it only closes the file.
func (s *n1Stream) close() error {
	w := s.async
	if w == nil {
		return s.file.Close()
	}
	s.mu.Lock()
	w.mu.Lock()
	already := w.closed
	w.closed = true
	w.mu.Unlock()
	if !already {
		close(w.queue)
	}
	s.mu.Unlock()
	<-w.done
	w.mu.Lock()
	dropped, first, last, werr := w.dropped, w.firstDrop, w.lastDrop, w.err
	w.mu.Unlock()
	var dropErr error
	if dropped > 0 {
		dropErr = fmt.Errorf("N1 raw stream queue overflow: %d records dropped (sequences %d..%d); the evidence is incomplete", dropped, first, last)
	}
	if already {
		return errors.Join(werr, dropErr)
	}
	return errors.Join(werr, dropErr, s.file.Close())
}

func (s *n1Stream) transportName() string {
	if s.streamOnly {
		return "unix-socket (external gzip observer; never fsynced)"
	}
	return "plain file (fsync at most every 1 s and at close)"
}
