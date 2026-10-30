package httpapi

import (
	"context"
	"log"
	"net/http"
	"sync/atomic"
	"time"
)

// net/http derives every request's context from its connection's, and cancels
// the connection's context whenever its background read of the socket fails —
// including a read deadline that a handler set and let expire. The socket stays
// usable, so the client keeps the keep-alive connection and every later request
// on it arrives with a context that is already canceled: anything that checks
// its context fails at once, for as long as the client keeps that connection.
//
// A request whose context is canceled before any handler has run is on such a
// connection (or its client has just gone, in which case nothing here matters).
// It is served with the cancellation detached — the lane budgets still bound
// it — and the connection is closed after the reply, so the client's next
// request opens a fresh one. HTTP/2 streams are left alone: there a canceled
// context means the stream itself was reset.
//
// The request is marked, because a lane without a budget has nothing left to
// bound it: admission refuses it there rather than start work that neither a
// deadline nor the client going away could end (see cancellationDetached).
func detachCanceledConnection(w http.ResponseWriter, r *http.Request) *http.Request {
	if r.ProtoMajor != 1 || r.Context().Err() == nil {
		return r
	}
	w.Header().Set("Connection", "close")
	noteCanceledConnection()
	return r.WithContext(context.WithValue(context.WithoutCancel(r.Context()), detachedCancellationKey{}, true))
}

type detachedCancellationKey struct{}

// cancellationDetached reports whether this request is being served with its
// connection's cancellation removed, so its client leaving cannot be observed.
func cancellationDetached(ctx context.Context) bool {
	detached, _ := ctx.Value(detachedCancellationKey{}).(bool)
	return detached
}

var canceledConnectionLogged atomic.Int64
var canceledConnectionCount atomic.Int64

func noteCanceledConnection() {
	count := canceledConnectionCount.Add(1)
	now := time.Now().UnixNano()
	last := canceledConnectionLogged.Load()
	if now-last < int64(time.Minute) || !canceledConnectionLogged.CompareAndSwap(last, now) {
		return
	}
	log.Printf("[network] warn: A request arrived on a connection whose context was already canceled; it was served and the connection closed (%d so far)", count)
}
