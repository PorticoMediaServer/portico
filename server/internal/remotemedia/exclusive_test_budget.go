package remotemedia

import (
	"context"
	"sync"
	"time"
)

const sourceTestDrain = 5 * time.Second
const sourceTestHold = 15 * time.Second

// ExclusiveTest is resource custody for an owner-initiated connection test,
// never source/actor permission. Existing operations drain without cancellation;
// new operations wait until Close or the bounded hold expires. The temporary
// serve must use Context and Operations, the saved source limits, and its own
// checked actor/configuration/process lease. It must physically stop on expiry.
type ExclusiveTest struct {
	budget     *OperationBudget
	life       context.Context
	cancel     context.CancelFunc
	operations *OperationBudget
	once       sync.Once
	// One setup phase follows the temporary serve. Every resolved leaf shares
	// its original pacing boundary; a leaf never starts an independent pacer.
	diagnosticMu                               sync.Mutex
	diagnosticNextRequest, diagnosticNextBytes time.Time
	diagnosticRequests                         int
}

func (t *ExclusiveTest) Context() context.Context     { return t.life }
func (t *ExclusiveTest) Operations() *OperationBudget { return t.operations }

func (b *OperationBudget) notifyLocked() {
	close(b.changed)
	b.changed = make(chan struct{})
}

// AcquireTest stops new admissions atomically before draining. A nested fresh
// metadata fence may finish in its existing permit, so a cap1 reader cannot
// deadlock against the drain. Another test never receives an independent hold.
func (b *OperationBudget) AcquireTest(ctx context.Context) (*ExclusiveTest, error) {
	if b == nil || b.life == nil || b.capacity < 1 || ctx == nil {
		return nil, ErrPolicy
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := b.life.Err(); err != nil {
		return nil, err
	}
	if current, ok := ctx.Value(operationBudgetKey{}).(*operationPermit); ok && current.budget == b && current.active.Load() {
		return nil, ErrBusy
	}
	test := &ExclusiveTest{budget: b}
	test.life, test.cancel = context.WithTimeout(ctx, sourceTestHold)
	b.mu.Lock()
	if b.exclusive != nil {
		b.mu.Unlock()
		test.cancel()
		return nil, ErrBusy
	}
	b.exclusive = test
	b.mu.Unlock()
	failed := true
	defer func() {
		if failed {
			test.Close()
		}
	}()
	drain, cancelDrain := context.WithTimeout(test.life, sourceTestDrain)
	defer cancelDrain()
	for {
		b.mu.Lock()
		active, changed := b.active, b.changed
		b.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := b.life.Err(); err != nil {
			return nil, err
		}
		if err := test.life.Err(); err != nil {
			return nil, err
		}
		if active == 0 {
			break
		}
		select {
		case <-changed:
		case <-drain.Done():
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, ErrBusy
		case <-b.life.Done():
			return nil, b.life.Err()
		}
	}
	stopSource := context.AfterFunc(b.life, test.cancel)
	test.operations = NewOperationBudget(test.life, b.capacity)
	context.AfterFunc(test.life, func() { test.Close(); stopSource() })
	failed = false
	return test, nil
}

// Close releases only this hold. It never cancels the saved source lifetime or
// an already admitted reader. The temporary test context ends before readers
// can resume, including on timeout, cancellation or source retirement.
func (t *ExclusiveTest) Close() {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.cancel != nil {
			t.cancel()
		}
		t.budget.mu.Lock()
		if t.budget.exclusive == t {
			t.budget.exclusive = nil
			t.budget.notifyLocked()
		}
		t.budget.mu.Unlock()
	})
}
