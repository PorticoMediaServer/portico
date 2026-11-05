package writer

import (
	"errors"
	"time"
)

var ErrQuantum = errors.New("writer quantum refused")

// Work is a conservative forecast including examined/rejected rows and all
// native, FK, index and receipt effects. It is not a second governor UnitBudget.
// Hold includes physical return. Zero in a step means no work in that dimension;
// zero in a complete limit means an unknown/unqualified recipe and refuses.
type Work struct {
	Rows     uint64
	VMOps    uint64
	Pages    uint64
	WALBytes uint64
	Hold     time.Duration
}

const MaxCommandHold = 25 * time.Millisecond

// Quantum is a single-turn prospective ledger, not a grant or a transaction.
// The private writer still validates GV's exact grant before each operation.
// Never share a Quantum across goroutines or writer turns.
type Quantum struct {
	remaining Work
	terminal  Work
	start     time.Time
	last      time.Time
	end       time.Time
}

// NewQuantum reserves the full terminal COMMIT/rollback-and-return cost before
// the first statement. BEGIN and its effects must be charged as a step too.
// Neither this constructor nor a timer can preempt an already running fsync.
func NewQuantum(limit, terminal Work, start time.Time) (*Quantum, error) {
	if start.IsZero() || limit.Rows == 0 || limit.VMOps == 0 || limit.Pages == 0 || limit.WALBytes == 0 ||
		limit.Hold <= 0 || limit.Hold > MaxCommandHold || terminal.Hold <= 0 || !fits(terminal, limit) {
		return nil, ErrQuantum
	}
	return &Quantum{remaining: limit, terminal: terminal, start: start, last: start, end: start.Add(limit.Hold)}, nil
}

// TryStep charges a forecast only if it AND terminal reserve fit, both in the
// cumulative forecast and the actual remaining wall time. A refusal changes no
// ledger fields. Call immediately before starting the indivisible operation.
func (q *Quantum) TryStep(step Work, now time.Time) error {
	if q == nil || step.Hold <= 0 || now.Before(q.last) || !fits(step, q.remaining) {
		return ErrQuantum
	}
	after := subtract(q.remaining, step)
	if !fits(q.terminal, after) {
		return ErrQuantum
	}
	// Subtraction avoids overflow in step.Hold + terminal.Hold and elapsed sums.
	elapsed := now.Sub(q.start)
	if elapsed > MaxCommandHold || elapsed < 0 {
		return ErrQuantum
	}
	if now.After(q.end) || q.end.Sub(now) < step.Hold || q.end.Sub(now)-step.Hold < q.terminal.Hold {
		return ErrQuantum
	}
	q.remaining = after
	q.last = now
	return nil
}

// Remaining includes the terminal reserve. Values are copies, never mutable
// authority or permission to exceed the admitted complete-command forecast.
func (q *Quantum) Remaining() Work {
	if q == nil {
		return Work{}
	}
	return q.remaining
}

func fits(a, b Work) bool {
	return a.Hold >= 0 && a.Rows <= b.Rows && a.VMOps <= b.VMOps && a.Pages <= b.Pages && a.WALBytes <= b.WALBytes && a.Hold <= b.Hold
}
func subtract(a, b Work) Work {
	return Work{a.Rows - b.Rows, a.VMOps - b.VMOps, a.Pages - b.Pages, a.WALBytes - b.WALBytes, a.Hold - b.Hold}
}
