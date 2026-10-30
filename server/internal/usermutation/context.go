package usermutation

import (
	"context"
	"database/sql"
)

type requestKey struct{}
type authorityKey struct{}

// WithAuthority supplies the current principal proof separately from mutable
// target checks, so an applied deletion can replay before target lookup.
func WithAuthority(ctx context.Context, prove func(context.Context, *sql.Tx) error) context.Context {
	return context.WithValue(ctx, authorityKey{}, prove)
}

func AuthorizeContext(ctx context.Context, tx *sql.Tx) error {
	if prove, ok := ctx.Value(authorityKey{}).(func(context.Context, *sql.Tx) error); ok {
		return prove(ctx, tx)
	}
	return nil
}

type request struct {
	binding  Binding
	envelope Envelope
}

// WithRequest carries an immutable authenticated request into its domain write.
// Domain adapters still re-prove authority in the actual writer transaction.
func WithRequest(ctx context.Context, binding Binding, envelope Envelope) context.Context {
	return context.WithValue(ctx, requestKey{}, request{binding, envelope})
}
func RequestFrom(ctx context.Context) (Binding, Envelope, bool) {
	if ctx == nil {
		return Binding{}, Envelope{}, false
	}
	r, ok := ctx.Value(requestKey{}).(request)
	return r.binding, r.envelope, ok
}
func CheckContext(ctx context.Context, tx *sql.Tx, now int64) (*Receipt, error) {
	b, e, ok := RequestFrom(ctx)
	if !ok {
		return nil, nil
	}
	if err := AuthorizeContext(ctx, tx); err != nil {
		return nil, err
	}
	return Check(ctx, tx, b, e, now)
}
func SaveContext(ctx context.Context, tx *sql.Tx, result Result, now int64) error {
	b, e, ok := RequestFrom(ctx)
	if !ok {
		return nil
	}
	return Save(ctx, tx, b, e, result, now)
}
