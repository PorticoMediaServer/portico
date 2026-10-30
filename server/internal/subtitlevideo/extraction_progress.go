package subtitlevideo

import (
	"context"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// A track may take a long time on a NAS as long as input keeps moving. The idle
// limit catches stalled storage/decoders; the outer limit bounds hostile input.
type progressingInput struct {
	subtitles.RenderInput
	mu     sync.Mutex
	timer  *time.Timer
	idle   time.Duration
	closed bool
}

func extractionProgress(ctx context.Context, input subtitles.RenderInput) (context.Context, *progressingInput, func()) {
	return extractionProgressLimits(ctx, input, 2*time.Minute, 2*time.Hour)
}
func extractionProgressLimits(ctx context.Context, input subtitles.RenderInput, idle, overall time.Duration) (context.Context, *progressingInput, func()) {
	ctx, outer := context.WithTimeout(ctx, overall)
	ctx, cancel := context.WithCancel(ctx)
	p := &progressingInput{RenderInput: input, idle: idle}
	p.timer = time.AfterFunc(idle, cancel)
	return ctx, p, func() {
		p.mu.Lock()
		p.closed = true
		p.timer.Stop()
		p.mu.Unlock()
		cancel()
		outer()
	}
}
func (p *progressingInput) ReadExtent(ctx context.Context, offset, n int64) ([]byte, error) {
	b, e := p.RenderInput.ReadExtent(ctx, offset, n)
	if e == nil && len(b) > 0 {
		p.mu.Lock()
		if !p.closed {
			p.timer.Reset(p.idle)
		}
		p.mu.Unlock()
	}
	return b, e
}
