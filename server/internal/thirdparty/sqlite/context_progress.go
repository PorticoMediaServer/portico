package sqlite

import (
	"context"
	"sync"
	"sync/atomic"

	"modernc.org/libc"
	sqlite3 "modernc.org/sqlite/lib"
)

// SQLite clears an idle interrupt before a VM starts. A cancellation arriving
// while arguments bind can therefore be lost by sqlite3_interrupt alone. A
// scoped progress callback observes the closed context channel from inside the
// VM; it also covers long Next calls after Query's interrupt watcher stops.
const cancellationProgressOps = 1000

type progressState struct{ done <-chan struct{} }

var progressStates sync.Map

// Lets synchronous native scalar callbacks observe a cancellation that arrived
// before VM activation (when SQLite may clear an idle interrupt). Entries have
// exactly the same lifetime as the connection's existing progress state.
var callbackProgressStates sync.Map // sqlite3* -> *progressState
var nextProgressToken atomic.Uint64

func cancellationProgress(_ *libc.TLS, token uintptr) int32 {
	value, ok := progressStates.Load(token)
	if !ok {
		return 0
	}
	select {
	case <-value.(*progressState).done:
		return 1
	default:
		return 0
	}
}

// database/sql serializes calls on a physical connection. SQLite invokes the
// callback synchronously inside that call, so state is read/written on the
// executing goroutine. Restoring the previous scope preserves nested callbacks.
type progressScope struct {
	conn     *conn
	previous <-chan struct{}
}

func (c *conn) watchProgress(ctx context.Context) progressScope {
	if ctx == nil || ctx.Done() == nil {
		return progressScope{}
	}
	if c.progress == nil {
		c.progress = &progressState{}
		for {
			token := uintptr(nextProgressToken.Add(1))
			if token == 0 {
				continue
			}
			if _, loaded := progressStates.LoadOrStore(token, c.progress); !loaded {
				c.progressToken = token
				break
			}
		}
		callbackProgressStates.Store(c.db, c.progress)
	}
	previous := c.progress.done
	c.progress.done = ctx.Done()
	sqlite3.Xsqlite3_progress_handler(c.tls, c.db, cancellationProgressOps, cFuncPointer(cancellationProgress), c.progressToken)
	return progressScope{conn: c, previous: previous}
}
func (scope progressScope) restore() {
	c := scope.conn
	if c == nil {
		return
	}
	c.progress.done = scope.previous
	if scope.previous == nil {
		sqlite3.Xsqlite3_progress_handler(c.tls, c.db, 0, 0, 0)
	} else {
		sqlite3.Xsqlite3_progress_handler(c.tls, c.db, cancellationProgressOps, cFuncPointer(cancellationProgress), c.progressToken)
	}
}

func (c *conn) clearProgress() {
	if c.progress == nil {
		return
	}
	sqlite3.Xsqlite3_progress_handler(c.tls, c.db, 0, 0, 0)
	progressStates.Delete(c.progressToken)
	callbackProgressStates.Delete(c.db)
	c.progress = nil
	c.progressToken = 0
}
