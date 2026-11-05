package search

import (
	"context"
	"errors"
	"math"
	"sync"
	"sync/atomic"
)

var ErrReadCapacity = errors.New("search request metadata capacity unavailable")
var ErrSessionKey = errors.New("invalid search session key")

// SearchRequests owns bounded in-flight request metadata, not GV resources.
// Composition supplies a qualified concurrency limit and charges this metadata
// separately. There is no retained session/user directory or user-count cap.
type SearchRequests struct {
	mu      sync.Mutex
	limit   int
	next    uint64
	closed  bool
	active  map[*SearchRequest]struct{}
	current map[string]*SearchRequest
}

// SearchRequest carries cancellation and response-generation identity. It
// owns no read connection, snapshot, worker or physical-retirement authority.
type SearchRequest struct {
	group      *SearchRequests
	session    string
	generation uint64
	ctx        context.Context
	cancel     context.CancelFunc
	current    atomic.Bool
}

func NewSearchRequests(maxActive int) (*SearchRequests, error) {
	if maxActive <= 0 {
		return nil, ErrReadCapacity
	}
	return &SearchRequests{limit: maxActive, active: make(map[*SearchRequest]struct{}), current: make(map[string]*SearchRequest)}, nil
}

// Begin immediately refuses contention/capacity. Supersession cancels the old
// request but keeps its metadata charged until Finish; cancellation is not
// proof that its SQL callback or other work has physically returned.
func (g *SearchRequests) Begin(ctx context.Context, session string) (*SearchRequest, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if session == "" || len(session) > 1024 {
		return nil, ErrSessionKey
	}
	if !g.mu.TryLock() {
		return nil, ErrReadCapacity
	}
	if g.closed || len(g.active) >= g.limit || g.next == math.MaxUint64 {
		g.mu.Unlock()
		return nil, ErrReadCapacity
	}
	g.next++
	child, cancel := context.WithCancel(ctx)
	r := &SearchRequest{group: g, session: session, generation: g.next, ctx: child, cancel: cancel}
	r.current.Store(true)
	old := g.current[session]
	if old != nil {
		old.current.Store(false)
	}
	g.current[session] = r
	g.active[r] = struct{}{}
	g.mu.Unlock()
	if old != nil {
		old.cancel()
	}
	return r, nil
}

func (r *SearchRequest) Context() context.Context { return r.ctx }
func (r *SearchRequest) Generation() uint64       { return r.generation }

// Current must be checked at the response boundary before Finish. Responses
// retain Generation to distinguish completions; existing client request
// ordering handles responses already in flight. No wire format is introduced,
// and no network wait retains a SQL snapshot.
func (r *SearchRequest) Current() bool { return r.current.Load() && r.ctx.Err() == nil }

func (r *SearchRequest) Cancel() { r.current.Store(false); r.cancel() }

// Finish is called by the private coordinator AFTER all request work/ReadScope
// callbacks have physically returned. It only releases metadata; GV Read owns
// its own tracked Rows/Tx/Conn closure and resource retirement independently.
// An older or duplicate Finish never removes a newer request for the session.
func (r *SearchRequest) Finish() {
	r.Cancel()
	g := r.group
	g.mu.Lock()
	delete(g.active, r)
	if g.current[r.session] == r {
		delete(g.current, r.session)
	}
	g.mu.Unlock()
}

// Close revokes new requests and cancels all current work. Existing requests
// remain charged and must Finish after physical return; no early refund.
func (g *SearchRequests) Close() {
	g.mu.Lock()
	g.closed = true
	pending := make([]*SearchRequest, 0, len(g.active))
	for r := range g.active {
		r.current.Store(false)
		pending = append(pending, r)
	}
	g.mu.Unlock()
	for _, r := range pending {
		r.cancel()
	}
}
