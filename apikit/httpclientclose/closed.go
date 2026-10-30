// Package httpclientclose distinguishes a canceled incoming request from a
// server deadline or a canceled authority lease whose request is still live.
package httpclientclose

import (
	"context"
	"errors"
	"net/http"
)

// Status is the conventional observation for a client that closed its request.
// It is not a server failure and must not feed timeout/error alerts.
const Status = 499

func Context(ctx context.Context) bool {
	return ctx != nil && errors.Is(ctx.Err(), context.Canceled)
}

// Writer finds the original incoming context through ordinary response
// wrappers. Child operation/authority contexts never classify the client.
func Writer(w http.ResponseWriter) bool {
	for depth := 0; w != nil && depth < 32; depth++ {
		if source, ok := w.(interface{ RequestContext() context.Context }); ok {
			return Context(source.RequestContext())
		}
		wrapper, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return false
		}
		w = wrapper.Unwrap()
	}
	return false
}
