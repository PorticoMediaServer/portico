package linearinput

import (
	"context"
	"errors"
	"io"
	"time"
)

// A shared HLS resource keeps its exact admitted URL/range. No partial body is
// cached, and retry time includes reads as well as connection acquisition.
func (g *Gateway) resourceData(res resource, rng string) ([]byte, string, string, error) {
	work, cancel := context.WithTimeout(g.ctx, g.retryWindow())
	defer cancel()
	limit := int64(maxSegment)
	if res.kind == "manifest" {
		limit = maxManifest
	}
	if res.kind == "key" {
		limit = 16
	}
	delay := time.Second
	for work.Err() == nil {
		client, response, err := g.fetch(work, res.url, rng)
		if err == nil {
			data, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
			_ = response.Body.Close()
			client.Close()
			g.untrack(client, response.Body)
			if int64(len(data)) > limit {
				return nil, "", "", ErrFormat
			}
			if readErr == nil && work.Err() == nil {
				location := res.url
				if response.Request != nil && response.Request.URL != nil {
					location = response.Request.URL.String()
				}
				contentRange := ""
				if response.StatusCode == 206 && res.kind == "media" {
					contentRange = response.Header.Get("Content-Range")
				}
				return data, location, contentRange, nil
			}
			err = readErr
		}
		// A refused/invalid representation is not turned into a network retry.
		if errors.Is(err, ErrFormat) {
			return nil, "", "", ErrFormat
		}
		if work.Err() != nil {
			break
		}
		timer := time.NewTimer(retryDelay(err, delay))
		select {
		case <-work.Done():
			timer.Stop()
		case <-timer.C:
		}
		delay = min(5*time.Second, delay*2)
	}
	if g.ctx.Err() != nil {
		return nil, "", "", ErrUnavailable
	}
	return nil, "", "", ErrRetryWindow
}
