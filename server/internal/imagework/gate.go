// Package imagework bounds the memory and CPU used by image decoding and
// rendering. Compressed upload sizes do not bound decoded pixel allocations.
package imagework

import (
	"context"
	"errors"
	"sync"
)

var ErrBusy = errors.New("image processing is busy")

type gate struct{ active, waiting chan struct{} }

func newGate() *gate { return &gate{active: make(chan struct{}, 1), waiting: make(chan struct{}, 256)} }

var shared = newGate()
var uploadBodies = &gate{active: make(chan struct{}, 4), waiting: make(chan struct{}, 256)}

// AcquireUploadBody bounds buffered originals separately from decoding: a slow
// uploader never holds the decoder slot other clients need for artwork.
func AcquireUploadBody(ctx context.Context) (func(), error) { return uploadBodies.acquire(ctx) }

// Acquire shares one decoder/rendering slot between avatars and artwork. The
// accepted image dimensions and formats stay unchanged. At most 256 callers
// wait; additional work receives a retryable refusal instead of multiplying
// full-size pixel buffers. Cancellation removes queued work immediately.
func Acquire(ctx context.Context) (func(), error) { return shared.acquire(ctx) }
func (g *gate) acquire(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	release := func() (func(), error) {
		if err := ctx.Err(); err != nil {
			<-g.active
			return nil, err
		}
		var once sync.Once
		return func() { once.Do(func() { <-g.active }) }, nil
	}
	select {
	case g.active <- struct{}{}:
		return release()
	default:
	}
	select {
	case g.waiting <- struct{}{}:
	default:
		return nil, ErrBusy
	}
	defer func() { <-g.waiting }()
	select {
	case g.active <- struct{}{}:
		return release()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
