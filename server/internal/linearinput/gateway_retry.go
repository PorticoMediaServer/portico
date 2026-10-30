package linearinput

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/remotemedia"
)

// acquiredBody owns the request context after acquisition. Its retry deadline
// has been withdrawn; the original source context and this body still retire it.
type acquiredBody struct {
	io.ReadCloser
	cancel context.CancelFunc
	once   sync.Once
	err    error
}

func (b *acquiredBody) Close() error {
	b.once.Do(func() { b.cancel(); b.err = b.ReadCloser.Close() })
	return b.err
}

func (g *Gateway) retryWindow() time.Duration {
	window := time.Duration(g.input.RetryWindowSeconds) * time.Second
	if window <= 0 {
		return time.Minute
	}
	return window
}

// openRetry applies the admitted retry window to initial acquisition as well as
// reconnects. The ordinary per-read timeout remains independent. A successful
// stream keeps running after the window, under the original source lifetime.
func (g *Gateway) openRetry(tsOnly bool) (*remotemedia.Client, *http.Response, *bufio.Reader, error) {
	until := time.Now().Add(g.retryWindow())
	delay := time.Duration(0)
	if tsOnly {
		delay = time.Second
	}
	for g.ctx.Err() == nil && time.Now().Before(until) {
		if delay > 0 {
			timer := time.NewTimer(min(delay, time.Until(until)))
			select {
			case <-g.ctx.Done():
				timer.Stop()
				return nil, nil, nil, ErrUnavailable
			case <-timer.C:
			}
		}
		if !time.Now().Before(until) {
			break
		}
		attempt, cancel := context.WithCancel(g.ctx)
		deadline := time.AfterFunc(time.Until(until), cancel)
		client, response, err := g.fetch(attempt, g.input.Locator, "")
		var reader *bufio.Reader
		invalid := false
		if err == nil {
			reader = bufio.NewReaderSize(response.Body, 4096)
			need := 7
			if tsOnly {
				need = 1
			}
			prefix, readErr := reader.Peek(need)
			if !tsOnly && len(prefix) >= 3 && bytes.Equal(prefix[:3], []byte{0xef, 0xbb, 0xbf}) {
				_, _ = reader.Discard(3)
				prefix, readErr = reader.Peek(need)
			}
			if readErr != nil || len(prefix) < need {
				err = ErrUnavailable
			} else if prefix[0] != 0x47 && (tsOnly || !bytes.Equal(prefix, []byte("#EXTM3U"))) {
				err, invalid = ErrFormat, true
			} else if !tsOnly && bytes.Equal(prefix, []byte("#EXTM3U")) {
				// The initial manifest is finite, so admit its complete bounded body
				// before withdrawing acquisition time. A slow prefix is not success.
				data, readErr := io.ReadAll(io.LimitReader(reader, maxManifest+1))
				if len(data) > maxManifest {
					err, invalid = ErrFormat, true
				} else if readErr != nil {
					err = ErrUnavailable
				} else {
					reader = bufio.NewReaderSize(bytes.NewReader(data), 4096)
				}
			}
		}
		stopped := deadline.Stop()
		if err == nil && stopped && attempt.Err() == nil {
			g.mu.Lock()
			if !g.closing && g.ctx.Err() == nil {
				original := response.Body
				body := &acquiredBody{ReadCloser: original, cancel: cancel}
				delete(g.bodies, original)
				g.bodies[body] = true
				response.Body = body
				g.mu.Unlock()
				return client, response, reader, nil
			}
			g.mu.Unlock()
		}
		cancel()
		if response != nil {
			_ = response.Body.Close()
		}
		if client != nil {
			client.Close()
			if response != nil {
				g.untrack(client, response.Body)
			}
		}
		// Unsupported initial content is not a transient outage. Reconnect retains
		// its existing bounded retry behavior when the source temporarily changes.
		if !tsOnly && (invalid || errors.Is(err, ErrFormat)) {
			return nil, nil, nil, ErrFormat
		}
		if delay == 0 {
			delay = time.Second
		} else {
			delay = min(5*time.Second, delay*2)
		}
		delay = retryDelay(err, delay)
	}
	if g.ctx.Err() != nil {
		return nil, nil, nil, ErrUnavailable
	}
	return nil, nil, nil, ErrRetryWindow
}
