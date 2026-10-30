package remotemedia

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
)

// OpenFromStart is the interactive sequential transport. It deliberately sends
// no Range and accepts only a complete representation starting at byte zero.
// It does not buffer, cache, seek, resume or silently replace a failed stream.
// The owner fences every returned extent before exposing it to its consumer.
func (s *ServeEndpoint) OpenFromStart(ctx context.Context, relative, etag string, size int64, reconnect func(context.Context) error) (io.ReadCloser, error) {
	if reconnect == nil || relative == "" || !cleanDAVRelative(relative) || strings.Contains(relative, ":") || size < 0 || etag != "" && !serveETag(etag) {
		return nil, ErrPolicy
	}
	ctx, release, err := s.budget.enter(ctx)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			release()
		}
	}()
	if err := reconnect(ctx); err != nil {
		return nil, err
	}
	requestContext := ctx
	ctx = context.WithValue(ctx, serveReconnectKey{}, func() error { return reconnect(requestContext) })
	target := s.root
	target.Path += relative
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, ErrPolicy
	}
	req.Header.Set("Authorization", s.authorization)
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "Portico/1")
	if etag != "" {
		req.Header.Set("If-Match", etag)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(err, ErrObservationChanged) {
			return nil, ErrObservationChanged
		}
		return nil, boundRequestError(ctx, err)
	}
	good := false
	defer func() {
		if !good {
			resp.Body.Close()
		}
	}()
	if resp.StatusCode == http.StatusPreconditionFailed || resp.StatusCode == http.StatusNotFound {
		return nil, ErrObservationChanged
	}
	if resp.StatusCode != http.StatusOK {
		return nil, serveResponseError(resp)
	}
	if resp.ContentLength != size || resp.Header.Get("Content-Range") != "" || resp.Header.Get("Content-Encoding") != "" || etag != "" && resp.Header.Get("ETag") != etag {
		return nil, ErrRepresentationRange
	}
	// The owner checks current authority after headers. Fresh backend stat here
	// could wait on the sole provider connection occupied by this live body.
	good = true
	transferred = true
	return retainOperationBody(ctx, resp.Body, release), nil
}
