package httpapi

import (
	"context"
	"errors"
	"io"
)

var errBudget = errors.New("private audio byte budget exhausted")

// authorityWriter checks the original grant before each converted output chunk.
// The shared verdict cache bounds database work and revocation latency.
type authorityWriter struct {
	w     io.Writer
	ctx   context.Context
	check func() error
}

func (a *authorityWriter) Write(p []byte) (int, error) {
	if err := a.ctx.Err(); err != nil {
		return 0, err
	}
	if err := a.check(); err != nil {
		return 0, err
	}
	return a.w.Write(p)
}

// A private response ends at its aggregate byte ceiling. V1 never waits in a
// media response for queue commit. After ordinary commit a fresh GET of the
// same grant is authorized without this private budget. CAF progressive queue
// playback has no preload/gapless guarantee (MUSIC Later, ruling17.2).
type budgetWriter struct {
	w      io.Writer
	ctx    context.Context
	check  func() error
	take   func(int64) int64
	refund func(int64)
}

func (b *budgetWriter) Write(p []byte) (int, error) {
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	if err := b.check(); err != nil {
		return 0, err
	}
	n := b.take(int64(len(p)))
	if n == 0 {
		return 0, errBudget
	}
	written, err := b.w.Write(p[:n])
	if int64(written) < n {
		b.refund(n - int64(written))
	}
	if err != nil {
		return written, err
	}
	if int64(written) != n {
		return written, io.ErrShortWrite
	}
	if n < int64(len(p)) {
		return written, errBudget
	}
	return written, nil
}
