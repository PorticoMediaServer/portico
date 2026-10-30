package subtitlevideo

import (
	"context"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"sync"
)

// Reuse bounded immutable source extents across segment decoders. Validation at
// each producer boundary still checks current source/configuration authority.
type cachedInput struct {
	subtitles.RenderInput
	mu    sync.Mutex
	cache map[[2]int64][]byte
	order [][2]int64
	bytes int
}

func cacheInput(in subtitles.RenderInput) subtitles.RenderInput {
	return &cachedInput{RenderInput: in, cache: map[[2]int64][]byte{}}
}
func (c *cachedInput) ReadExtent(ctx context.Context, offset, length int64) ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return nil, e
	}
	key := [2]int64{offset, length}
	if v, ok := c.cache[key]; ok {
		return append([]byte(nil), v...), nil
	}
	v, e := c.RenderInput.ReadExtent(ctx, offset, length)
	if e != nil {
		return nil, e
	}
	for c.bytes+len(v) > 8<<20 && len(c.order) > 0 {
		old := c.order[0]
		c.order = c.order[1:]
		c.bytes -= len(c.cache[old])
		delete(c.cache, old)
	}
	if len(v) <= 8<<20 {
		c.cache[key] = append([]byte(nil), v...)
		c.order = append(c.order, key)
		c.bytes += len(v)
	}
	return v, nil
}
func (c *cachedInput) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cache = nil
	c.order = nil
	c.bytes = 0
	return c.RenderInput.Close()
}
