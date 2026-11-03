package dbwork

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"time"
)

// A request re-proves who is calling inside each transaction it opens: the
// session family, the account and profile, the device and the profile's
// restrictions. A title page opens about nine transactions and ran the same
// handful of authority queries 8 to 18 times; on a busy server those checks
// were a fifth of all CPU, more than SQLite spent executing the reads.
//
// The request memo lets a read-only snapshot reuse an answer the same request
// already proved moments ago. It never applies inside a gated write: a
// transaction that changes data still proves its authority itself, in the
// transaction it commits. Any write the request starts empties the memo, so a
// request that changes who it is (a profile switch, a sign-out) re-proves
// everything after. An answer is reused for at most RequestMemoLifetime, and
// only successful answers are kept.
const RequestMemoLifetime = 2 * time.Second

var requestMemoHits atomic.Uint64

// RequestMemoHits counts authority answers a request reused instead of proving again.
func RequestMemoHits() uint64 { return requestMemoHits.Load() }

type requestMemoKey struct{}

type requestMemo struct {
	mu      sync.Mutex
	entries map[string]requestMemoEntry
}

type requestMemoEntry struct {
	value any
	at    time.Time
}

// WithRequestMemo gives ctx an empty request memo. HTTP admission calls it once
// per request.
func WithRequestMemo(ctx context.Context) context.Context {
	if ctx == nil {
		return ctx
	}
	return context.WithValue(ctx, requestMemoKey{}, &requestMemo{entries: map[string]requestMemoEntry{}})
}

func requestMemoFrom(ctx context.Context) *requestMemo {
	if ctx == nil {
		return nil
	}
	memo, _ := ctx.Value(requestMemoKey{}).(*requestMemo)
	return memo
}

// forgetRequestMemo empties ctx's memo; every gated write does it.
func forgetRequestMemo(ctx context.Context) {
	if memo := requestMemoFrom(ctx); memo != nil {
		memo.mu.Lock()
		clear(memo.entries)
		memo.mu.Unlock()
	}
}

// RequestMemoApplies reports whether RememberForRequest would keep an answer
// for tx: inside a request, in a read-only snapshot dbwork opened. A caller
// with a cheaper single-row query for one-off use can choose it otherwise.
func RequestMemoApplies(ctx context.Context, tx *sql.Tx) bool {
	return requestMemoFrom(ctx) != nil && readSnapshotTx(tx)
}

// RememberForRequest returns the answer this request already proved for key, or
// proves it with fn and keeps a successful answer. It proves afresh outside a
// request, outside a read-only snapshot dbwork opened, and inside any write. clone copies a
// kept value before it is handed out, so no caller can change another's answer;
// nil means the value has no shared parts.
func RememberForRequest[T any](ctx context.Context, tx *sql.Tx, key string, clone func(T) T, fn func() (T, error)) (T, error) {
	memo := requestMemoFrom(ctx)
	if memo == nil || !readSnapshotTx(tx) {
		return fn()
	}
	memo.mu.Lock()
	entry, found := memo.entries[key]
	memo.mu.Unlock()
	if found && time.Since(entry.at) < RequestMemoLifetime {
		requestMemoHits.Add(1)
		value := entry.value.(T)
		if clone != nil {
			value = clone(value)
		}
		return value, nil
	}
	value, err := fn()
	if err != nil {
		return value, err
	}
	kept := value
	if clone != nil {
		kept = clone(value)
	}
	memo.mu.Lock()
	memo.entries[key] = requestMemoEntry{value: kept, at: time.Now()}
	memo.mu.Unlock()
	return value, nil
}

// readSnapshots holds the read-only transactions dbwork opened and has not yet
// ended. Only those may reuse a remembered answer: a transaction opened any
// other way might have written, and must see its own writes.
var readSnapshots sync.Map // *sql.Tx -> *snapshotMemo

func registerReadSnapshot(tx *sql.Tx) func() {
	readSnapshots.Store(tx, &snapshotMemo{})
	return func() { readSnapshots.Delete(tx) }
}

func readSnapshotTx(tx *sql.Tx) bool {
	if tx == nil {
		return false
	}
	if _, write := liveWrites.Load(tx); write {
		return false
	}
	_, ok := readSnapshots.Load(tx)
	return ok
}

// snapshotMemo holds answers computed inside one read snapshot.
type snapshotMemo struct {
	mu      sync.Mutex
	entries map[string]any
}

var snapshotMemoHits atomic.Uint64

// SnapshotMemoHits counts reads a snapshot answered from RememberInSnapshot.
func SnapshotMemoHits() uint64 { return snapshotMemoHits.Load() }

// RememberInSnapshot returns what fn answered for key earlier in the same
// read-only snapshot, or asks fn and keeps a successful answer for the rest of
// that snapshot. A snapshot sees one state of the database, so a read repeated
// in it can only give the same answer: unlike RememberForRequest this changes
// nothing a caller can observe, and it applies to any read, not only authority.
// Outside a read-only snapshot dbwork opened (a write, a raw handle) fn runs
// every time. clone copies a kept value before it is handed out; nil means it
// has no shared parts.
func RememberInSnapshot[T any](tx *sql.Tx, key string, clone func(T) T, fn func() (T, error)) (T, error) {
	if tx == nil {
		return fn()
	}
	if _, write := liveWrites.Load(tx); write {
		return fn()
	}
	value, ok := readSnapshots.Load(tx)
	if !ok {
		return fn()
	}
	memo := value.(*snapshotMemo)
	memo.mu.Lock()
	kept, found := memo.entries[key]
	memo.mu.Unlock()
	if found {
		snapshotMemoHits.Add(1)
		answer := kept.(T)
		if clone != nil {
			answer = clone(answer)
		}
		return answer, nil
	}
	answer, err := fn()
	if err != nil {
		return answer, err
	}
	stored := answer
	if clone != nil {
		stored = clone(answer)
	}
	memo.mu.Lock()
	if memo.entries == nil {
		memo.entries = map[string]any{}
	}
	memo.entries[key] = stored
	memo.mu.Unlock()
	return answer, nil
}
