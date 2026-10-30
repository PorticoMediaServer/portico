package dbwork

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/supervise"
)

// StrictEnv makes accidental transaction nesting fail loudly instead of quietly
// waiting on the write gate. Tests set it; production leaves it unset so a
// mistake degrades to a slow write rather than a panic in front of a viewer.
const StrictEnv = "PORTICO_DBWORK_STRICT"

// ErrNested reports a second gated write transaction begun while the calling
// context already holds one. It is returned only in strict mode; otherwise the
// helpers reuse the transaction already in flight.
var ErrNested = errors.New("dbwork: nested write transaction")

// ErrAborted reports a commit refused because an inner reuse of the same
// transaction rolled back. The outer owner must not commit a partial result it
// was told to discard.
var ErrAborted = errors.New("dbwork: transaction aborted by a nested writer")

// ErrTransactionContext refuses a missing or conflicting transaction carrier.
var ErrTransactionContext = errors.New("dbwork: transaction context is invalid or mismatched")

func strict() bool { return os.Getenv(StrictEnv) == "1" }

type writeKey struct{}
type snapshotKey struct{}

// carried is the gated transaction a context passes down so a helper called
// deeper in the same request reuses it instead of opening a second one. This is
// the whole re-entrancy story: one writer, one transaction, one gate hold.
type carried struct {
	tx      *sql.Tx
	class   Class
	mu      sync.Mutex
	aborted bool
	depth   int
	// authority records that a write joined to this transaction was itself
	// authority-bearing, even when the owner's class is not.
	authority bool
}

func (c *carried) abort() {
	c.mu.Lock()
	c.aborted = true
	c.mu.Unlock()
}

func (c *carried) failed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.aborted
}

// carriedWrite returns the gated transaction this context already holds.
func carriedWrite(ctx context.Context) *carried {
	if ctx == nil {
		return nil
	}
	value, _ := ctx.Value(writeKey{}).(*carried)
	return value
}

// WithWrite attaches an already-open gated transaction to ctx. It exists for
// call sites that own a transaction from an older shape and want the helpers
// below to reuse it; new code gets this for free from WithWriteTx.
func WithWrite(ctx context.Context, tx *sql.Tx, class Class) context.Context {
	if tx == nil {
		return ctx
	}
	return context.WithValue(ctx, writeKey{}, &carried{tx: tx, class: class})
}

// InWriteTx reports whether ctx already holds a gated write transaction.
func InWriteTx(ctx context.Context) bool { return carriedWrite(ctx) != nil }

// WriteClass returns the class of the transaction ctx holds, if any.
func WriteClass(ctx context.Context) (Class, bool) {
	if held := carriedWrite(ctx); held != nil {
		return held.class, true
	}
	return ClassInteractive, false
}

// Snapshot returns the read-only snapshot transaction ctx carries, if any. Read
// helpers pick it up transparently so a multi-query projection sees one
// consistent state without any call site having to thread a transaction through
// its own signatures.
func Snapshot(ctx context.Context) *sql.Tx {
	if ctx == nil {
		return nil
	}
	if held := carriedWrite(ctx); held != nil {
		// A write transaction is a stronger snapshot than a read-only one, and
		// reading outside it would miss this request's own uncommitted rows.
		return held.tx
	}
	value, _ := ctx.Value(snapshotKey{}).(*sql.Tx)
	return value
}

// withSnapshot attaches a read-only transaction to ctx.
func withSnapshot(ctx context.Context, tx *sql.Tx) context.Context {
	return context.WithValue(ctx, snapshotKey{}, tx)
}

// BindSnapshot carries the exact supplied transaction for a synchronous local
// callback. It opens nothing and grants no authority. A live gated writer keeps
// its actual shared writer/class and change tracking; other transactions receive
// only a read snapshot carrier. A foreign transaction already in ctx is refused.
//
// The result is ephemeral: do not retain it as an operation/reader lifetime.
// Binding neither owns nor extends the SQL transaction or caller's context.
func BindSnapshot(ctx context.Context, tx *sql.Tx) (context.Context, error) {
	if ctx == nil || tx == nil {
		return nil, ErrTransactionContext
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if held := Snapshot(ctx); held != nil && held != tx {
		return nil, ErrTransactionContext
	}
	// Check the read carrier too: Snapshot prefers a writer, which must not
	// conceal a conflicting read snapshot nested in the same context.
	if held, _ := ctx.Value(snapshotKey{}).(*sql.Tx); held != nil && held != tx {
		return nil, ErrTransactionContext
	}
	if value, ok := liveWrites.Load(tx); ok {
		w := value.(*Write)
		w.mu.Lock()
		defer w.mu.Unlock()
		if w.done || w.owned == nil {
			return nil, sql.ErrTxDone
		}
		if w.owned.failed() {
			return nil, ErrAborted
		}
		bound := context.WithValue(ctx, writeKey{}, w.owned)
		if w.changes != nil {
			bound = context.WithValue(bound, changeKey{}, w.changes)
		}
		return bound, nil
	}
	if carriedWrite(ctx) != nil {
		// Preserve an existing legacy WithWrite carrier; never manufacture a
		// write class for an untracked transaction passed without that carrier.
		return ctx, nil
	}
	return withSnapshot(ctx, tx), nil
}

type classKey struct{}

// WithClass records the semantic class of the work a context represents. HTTP
// admission sets it from the matched route; a background loop sets it once at the
// top. Shared packages that serve both then classify their writes correctly
// without having to know who called them.
func WithClass(ctx context.Context, class Class) context.Context {
	if !class.Valid() {
		return ctx
	}
	return context.WithValue(ctx, classKey{}, class)
}

// ClassFrom returns the class ctx carries, or fallback when it carries none. A
// package used by both a request handler and a bulk loop must classify with this
// rather than guessing, because the same function is correct at two priorities.
func ClassFrom(ctx context.Context, fallback Class) Class {
	if ctx != nil {
		if class, ok := ctx.Value(classKey{}).(Class); ok && class.Valid() {
			return class
		}
	}
	if !fallback.Valid() {
		return ClassInteractive
	}
	return fallback
}

type ownerKey struct{}

// maxOwnerKey bounds what one owner key may cost the gate. The HTTP keys are a
// short prefix and a digest or an address, well inside it.
const maxOwnerKey = 128

// WithOwner records the fairness key of whoever the work is for. HTTP admission
// sets it from the same bounded identity its lanes already share load by: a
// checked credential's device, or the peer address before a credential is
// checked. The gate uses it only to rotate between owners and to decide whose
// ticket a full pool refuses; it grants nothing. Background work uses its
// supervised operation name instead; work without either identity shares one
// conservative anonymous supervisor allowance.
func WithOwner(ctx context.Context, owner string) context.Context {
	if ctx == nil || owner == "" {
		return ctx
	}
	if len(owner) > maxOwnerKey {
		owner = owner[:maxOwnerKey]
	}
	return context.WithValue(ctx, ownerKey{}, owner)
}

func ownerFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	owner, _ := ctx.Value(ownerKey{}).(string)
	return owner
}

// backgroundOwnerFrom uses the actual supervised operation, not a request owner
// inherited by a background job. Legacy work without a supervised context
// shares the anonymous allowance; request owners cannot multiply its slots.
func backgroundOwnerFrom(ctx context.Context) string {
	if operation := supervise.Operation(ctx); operation != "" {
		return "supervisor:" + operation
	}
	return ""
}
