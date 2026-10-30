package remotemedia

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
)

// OperationBudget is the single per-remote concurrent-operation ceiling. It
// owns no actor/operation permission and does not count upstream idle sockets.
// The source process lifetime and normalized profile supply its actual bounds.
type OperationBudget struct {
	life      context.Context
	capacity  int
	changed   chan struct{}
	exclusive *ExclusiveTest
	mu        sync.Mutex
	active    int
	peak      int
}

type OperationCounts struct{ Capacity, Active, Peak int }
type operationBudgetKey struct{}
type operationPermit struct {
	budget *OperationBudget
	active atomic.Bool
}

// NewOperationBudget rejects unknown capacity/lifetime without a fallback.
func NewOperationBudget(life context.Context, capacity int) *OperationBudget {
	if life == nil || capacity < 1 || capacity > 256 {
		return nil
	}
	return &OperationBudget{life: life, capacity: capacity, changed: make(chan struct{})}
}

func (b *OperationBudget) Counts() OperationCounts {
	if b == nil {
		return OperationCounts{}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return OperationCounts{Capacity: b.capacity, Active: b.active, Peak: b.peak}
}

// Context is resource lifetime only, never an actor/session or content grant.
// A provisional process sharing this source's budget must physically retire
// with that budget instead of outliving a revoked source/profile incarnation.
func (b *OperationBudget) Context() context.Context {
	if b == nil {
		return nil
	}
	return b.life
}

// Enter admits one provider operation for this budget and returns the permit
// context plus its release. Release it exactly once, after the operation and
// any response body it owns end. A nested fence inside the same permit reuses
// it, exactly as the source process's own callers do; the permit is resource
// custody only, never actor, target or content authority.
func (b *OperationBudget) Enter(ctx context.Context) (context.Context, func(), error) {
	return b.enter(ctx)
}

func (b *OperationBudget) enter(ctx context.Context) (context.Context, func(), error) {
	if b == nil || b.life == nil || b.capacity < 1 || ctx == nil {
		return nil, nil, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if err := b.life.Err(); err != nil {
		return nil, nil, err
	}
	// A reconnect's metadata fences run sequentially inside the operation that
	// owns this private context. They borrow its slot; no second local pool or
	// cap1 self-deadlock. A returned/escaped old context cannot revive a permit.
	if current, ok := ctx.Value(operationBudgetKey{}).(*operationPermit); ok && current.budget == b && current.active.Load() {
		return ctx, func() {}, nil
	}
	for {
		b.mu.Lock()
		if err := ctx.Err(); err != nil {
			b.mu.Unlock()
			return nil, nil, err
		}
		if err := b.life.Err(); err != nil {
			b.mu.Unlock()
			return nil, nil, err
		}
		if b.exclusive == nil && b.active < b.capacity {
			b.active++
			b.peak = max(b.peak, b.active)
			b.mu.Unlock()
			break
		}
		changed := b.changed
		b.mu.Unlock()
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-b.life.Done():
			return nil, nil, b.life.Err()
		}
	}
	permit := &operationPermit{budget: b}
	permit.active.Store(true)
	life, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(b.life, cancel)
	var once sync.Once
	release := func() {
		once.Do(func() {
			permit.active.Store(false)
			stop()
			cancel()
			b.mu.Lock()
			b.active--
			b.notifyLocked()
			b.mu.Unlock()
		})
	}
	return context.WithValue(life, operationBudgetKey{}, permit), release, nil
}

// admittedBody retains the same operation through progressive consumption.
// Child close/EOF/cancellation ends only that stream's capacity, not the source.
type admittedBody struct {
	body    io.ReadCloser
	release func()
	once    sync.Once
	stop    func() bool
	mu      sync.Mutex
	end     error
}

func retainOperationBody(ctx context.Context, body io.ReadCloser, release func()) io.ReadCloser {
	b := &admittedBody{body: body, release: release}
	// Set the stop hook before the callback can use it.
	ready := make(chan struct{})
	b.stop = context.AfterFunc(ctx, func() { <-ready; b.Close() })
	close(ready)
	return b
}

func (b *admittedBody) Close() error {
	var err error
	b.once.Do(func() {
		b.stop()
		b.mu.Lock()
		if b.end == nil {
			b.end = io.ErrClosedPipe
		}
		b.mu.Unlock()
		err = b.body.Close()
		b.release()
	})
	return err
}
func (b *admittedBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	end := b.end
	b.mu.Unlock()
	if end != nil {
		return 0, end
	}
	n, err := b.body.Read(p)
	if err != nil {
		b.mu.Lock()
		if b.end == nil {
			b.end = err
		}
		b.mu.Unlock()
		b.Close()
	}
	return n, err
}
