package writer

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func asyncControlStream(t *testing.T) (*n1Stream, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "raw.ndjson")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &n1Stream{file: file, origin: time.Now(), base: map[string]any{"scope": "control"}}
	s.enableAsync()
	return s, path
}

func readStreamRecords(t *testing.T, path string) []map[string]any {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var out []map[string]any
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 4<<20)
	for sc.Scan() {
		var r map[string]any
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

// Every record from many appenders reaches the file once close() has joined the
// writer, in sequence order, with the final record last.
func TestN1AsyncStreamKeepsEveryRecordInSequenceOrder(t *testing.T) {
	s, path := asyncControlStream(t)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 250; i++ {
				if err := s.append("scheduler", map[string]any{"i": i}); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := s.append("final", map[string]any{"complete": true, "stream_writer": s.writerStats()}); err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	records := readStreamRecords(t, path)
	if len(records) != 8*250+1 {
		t.Fatalf("lost records: %d", len(records))
	}
	for i, r := range records {
		if r["sequence"] != float64(i+1) {
			t.Fatalf("record %d has sequence %v: order broken", i, r["sequence"])
		}
	}
	if records[len(records)-1]["kind"] != "final" {
		t.Fatal("the final record must be last")
	}
	if err := s.close(); err != nil {
		t.Fatal("close is idempotent", err)
	}
}

// at_ns is the event time, taken BEFORE the stream lock: holding the lock makes the
// appender wait, but its stamp (and the recorded before-to-after-lock wait) show it.
func TestN1AsyncStreamStampsTheEventBeforeTheLock(t *testing.T) {
	s, path := asyncControlStream(t)
	var now atomic.Int64
	now.Store(1000)
	stamped := make(chan struct{}, 1)
	s.clock = func() int64 {
		select {
		case stamped <- struct{}{}: // first read is the event stamp
		default:
		}
		return now.Load()
	}
	s.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- s.append("attempt", map[string]any{"k": 1}) }()
	<-stamped       // the appender has taken its event time (1000) and now waits for the lock
	now.Store(9000) // time passes while the lock is held
	s.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := s.close(); err != nil {
		t.Fatal(err)
	}
	r := readStreamRecords(t, path)
	if len(r) != 1 || r[0]["at_ns"] != float64(1000) {
		t.Fatalf("at_ns must be the pre-lock event time 1000: %v", r)
	}
	if got := s.async.maxLockWait; got != 8000 {
		t.Fatalf("before-to-after-lock wait must be visible as 8000 ns, got %d", got)
	}
}

// A sink that never returns (a stuck observer or disk) cannot block the caller,
// no lock is held across it, and a full queue is an EXPLICIT failure, not a silent
// drop. Releasing the sink lets close() join and report the overflow.
func TestN1AsyncStreamFullQueueFailsExplicitlyAndNeverBlocks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.ndjson")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	s := &n1Stream{file: file, origin: time.Now(), base: map[string]any{}}
	release := make(chan struct{})
	var once sync.Once
	s.startAsync(4, func(b []byte) error { <-release; _, err := file.Write(b); return err }, func() error { return nil })
	finished := make(chan struct{})
	var firstErr error
	go func() {
		defer close(finished)
		for i := 0; i < 64; i++ { // far more than the 4-record queue plus the one in the sink
			if err := s.append("scheduler", nil); err != nil && firstErr == nil {
				firstErr = err
			}
		}
	}()
	select {
	case <-finished: // liveness only: the appender never waits for the stuck sink
	case <-time.After(20 * time.Second):
		t.Fatal("append blocked behind a stuck sink")
	}
	if firstErr == nil || !strings.Contains(firstErr.Error(), "queue overflow") {
		t.Fatalf("a full queue must fail explicitly, got %v", firstErr)
	}
	// No lock is held across the sink: the stats lock and the stream lock are free.
	if s.mu.TryLock() {
		s.mu.Unlock()
	} else {
		t.Fatal("stream mutex held while the sink is stuck")
	}
	once.Do(func() { close(release) })
	err = s.close()
	if err == nil || !strings.Contains(err.Error(), "queue overflow") {
		t.Fatalf("close must report the overflow too: %v", err)
	}
	stats := s.writerStats().(map[string]any)
	if stats["dropped_records"].(uint64) == 0 {
		t.Fatal("drops must be counted", stats)
	}
	if records := readStreamRecords(t, path); len(records) == 0 || len(records) >= 64 {
		t.Fatalf("expected a partial prefix with a visible sequence gap, got %d records", len(records))
	}
}

// A sink error is explicit: the next append and close() return it.
func TestN1AsyncStreamWriterErrorsSurface(t *testing.T) {
	path := filepath.Join(t.TempDir(), "raw.ndjson")
	file, _ := os.Create(path)
	s := &n1Stream{file: file, origin: time.Now(), base: map[string]any{}}
	failed := make(chan struct{})
	s.startAsync(16, func([]byte) error { defer close(failed); return os.ErrClosed }, func() error { return nil })
	if err := s.append("attempt", nil); err != nil {
		t.Fatal(err)
	}
	<-failed
	for i := 0; i < 1000; i++ { // the writer records the error shortly after the sink returns
		if s.append("attempt", nil) != nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err := s.close(); err == nil {
		t.Fatal("a failed sink must fail close")
	}
}
